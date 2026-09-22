// 限流验收：覆盖单实例与多实例两种拓扑。
//
// 关键点是「同一个用户在多个实例上并发投票时，总放行数不能超过配置额度」。
// 如果限流 ZSET 的 member 用进程内自增序号，两个实例在同一毫秒会生成相同的
// member，ZADD 把两条记录合并成一条，实际放行数就会超过额度。
//
// 用法（两个实例需已启动，见 scripts/verify_ratelimit.sh）：
//
//	go run ./ratelimit
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var client = &http.Client{Timeout: 20 * time.Second}

type apiResp struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

func call(base, method, path, token string, body interface{}) (apiResp, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return apiResp{}, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return apiResp{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return apiResp{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out apiResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return apiResp{}, fmt.Errorf("decode %q: %w", string(raw), err)
	}
	return out, nil
}

func mustCall(base, method, path, token string, body interface{}) apiResp {
	r, err := call(base, method, path, token, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "请求失败 %s %s: %v\n", method, path, err)
		os.Exit(1)
	}
	return r
}

// setupUser 注册并登录，返回 token 与新建的帖子 id
func setupUser(base, username string) (string, string) {
	mustCall(base, "POST", "/signup", "", map[string]string{
		"username": username, "password": "password123", "re_password": "password123",
	})
	login := mustCall(base, "POST", "/login", "", map[string]string{
		"username": username, "password": "password123",
	})
	var d struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(login.Data, &d)
	created := mustCall(base, "POST", "/post", d.Token, map[string]interface{}{
		"community_id": 1, "title": "ratelimit probe", "content": "x",
	})
	var p struct {
		PostID string `json:"post_id"`
	}
	_ = json.Unmarshal(created.Data, &p)
	return d.Token, p.PostID
}

func vote(base, token, postID string, direction int) (int, error) {
	r, err := call(base, "POST", "/vote", token, map[string]string{
		"post_id": postID, "direction": strconv.Itoa(direction),
	})
	if err != nil {
		return 0, err
	}
	return r.Code, nil
}

var failures int

func ok(format string, a ...interface{}) { fmt.Printf("  \033[32m✅ "+format+"\033[0m\n", a...) }
func bad(format string, a ...interface{}) {
	failures++
	fmt.Printf("  \033[31m❌ "+format+"\033[0m\n", a...)
}
func info(format string, a ...interface{}) { fmt.Printf("     "+format+"\n", a...) }

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	baseA := os.Getenv("APP_A")
	if baseA == "" {
		baseA = "http://127.0.0.1:9090/api/v1"
	}
	baseB := os.Getenv("APP_B")
	if baseB == "" {
		baseB = "http://127.0.0.1:9091/api/v1"
	}
	limit := envInt("RL_LIMIT", 10)
	windowMs := envInt("RL_WINDOW_MS", 1000)
	window := time.Duration(windowMs) * time.Millisecond
	const burst = 120

	stamp := time.Now().UnixNano()
	fmt.Printf("实例 A=%s  实例 B=%s\n", baseA, baseB)
	fmt.Printf("配置额度: %d 次 / %d ms\n\n", limit, windowMs)

	// ---------------------------------------------------------------- 多实例并发
	fmt.Println("1. 同一个用户并发打两个实例（member 冲突会在这里超发）")
	tokenA, postID := setupUser(baseA, fmt.Sprintf("rl_%d", stamp))

	var allowed, limited, other int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			base := baseA
			dir := 1
			if i%2 == 1 {
				base, dir = baseB, -1
			}
			<-start
			code, err := vote(base, tokenA, postID, dir)
			switch {
			case err != nil:
				atomic.AddInt64(&other, 1)
			case code == 1000:
				atomic.AddInt64(&allowed, 1)
			case code == 1012:
				atomic.AddInt64(&limited, 1)
			default:
				atomic.AddInt64(&other, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	info("并发 %d 次：放行=%d 限流=%d 其他失败=%d", burst, allowed, limited, other)
	if other > 0 {
		bad("出现 %d 个非限流失败，测试环境不稳定", other)
	}
	if allowed <= int64(limit) {
		ok("放行 %d <= 额度 %d，未超发", allowed, limit)
	} else {
		bad("放行 %d > 额度 %d —— member 冲突导致超发", allowed, limit)
	}
	if allowed == int64(limit) {
		ok("恰好用满额度（%d）", limit)
	} else if allowed < int64(limit) {
		info("放行数小于额度，可能并发窗口被拉长（不影响正确性）")
	}

	// ---------------------------------------------------------------- 用户隔离
	fmt.Println("\n2. 另一个用户不受影响（限流必须按用户维度隔离）")
	tokenB, postB := setupUser(baseB, fmt.Sprintf("rl_%d_other", stamp))
	code, err := vote(baseA, tokenB, postB, 1)
	if err == nil && code == 1000 {
		ok("另一用户立刻请求被放行（code=1000）")
	} else {
		bad("另一用户也受限（code=%d err=%v），说明限流没按用户隔离", code, err)
	}

	// ---------------------------------------------------------------- 窗口恢复
	fmt.Println("\n3. 窗口结束后额度恢复")
	time.Sleep(window + 200*time.Millisecond)
	code, err = vote(baseA, tokenA, postID, 1)
	if err == nil && code == 1000 {
		ok("原用户额度已恢复（code=1000）")
	} else {
		bad("窗口已过仍被限流（code=%d err=%v）", code, err)
	}

	fmt.Println()
	if failures == 0 {
		fmt.Println("  \033[32m全部通过 ✅\033[0m")
		return
	}
	fmt.Printf("  \033[31m有 %d 项未通过 ❌\033[0m\n", failures)
	os.Exit(1)
}
