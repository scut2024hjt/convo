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

const base = "http://127.0.0.1:9090/api/v1"

var httpClient = &http.Client{Timeout: 15 * time.Second}

func call(method, path, token string, body interface{}, out interface{}) (int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %q: %w", string(raw), err)
		}
	}
	return resp.StatusCode, nil
}

type apiResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func signupLogin(username string) (string, int64, error) {
	var r apiResp
	_, err := call("POST", "/signup", "", map[string]string{
		"username": username, "password": "password123", "re_password": "password123",
	}, &r)
	if err != nil {
		return "", 0, err
	}
	r = apiResp{}
	_, err = call("POST", "/login", "", map[string]string{
		"username": username, "password": "password123",
	}, &r)
	if err != nil {
		return "", 0, err
	}
	if r.Code != 1000 {
		return "", 0, fmt.Errorf("login failed: code=%d msg=%s", r.Code, r.Msg)
	}
	var data struct {
		UserID string `json:"user_id"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(r.Data, &data); err != nil {
		return "", 0, err
	}
	id, _ := strconv.ParseInt(data.UserID, 10, 64)
	return data.Token, id, nil
}

func createPost(token string) (int64, error) {
	var r apiResp
	_, err := call("POST", "/post", token, map[string]interface{}{
		"community_id": 1, "title": "e2e post", "content": "e2e content",
	}, &r)
	if err != nil {
		return 0, err
	}
	if r.Code != 1000 {
		return 0, fmt.Errorf("create post failed: code=%d msg=%s", r.Code, r.Msg)
	}
	// the handler returns data as {"post_id": "..."} or a bare id
	var obj struct {
		PostID string `json:"post_id"`
	}
	if err := json.Unmarshal(r.Data, &obj); err == nil && obj.PostID != "" {
		return strconv.ParseInt(obj.PostID, 10, 64)
	}
	var n int64
	if err := json.Unmarshal(r.Data, &n); err != nil {
		return 0, fmt.Errorf("unexpected create post data: %s", string(r.Data))
	}
	return n, nil
}

func postDetail(postID int64) (int64, string, error) {
	var r apiResp
	_, err := call("GET", "/post/"+strconv.FormatInt(postID, 10), "", nil, &r)
	if err != nil {
		return 0, "", err
	}
	var data struct {
		Title   string `json:"title"`
		VoteNum int64  `json:"vote_num"`
	}
	if err := json.Unmarshal(r.Data, &data); err != nil {
		return 0, "", fmt.Errorf("decode detail %s: %w", string(r.Data), err)
	}
	return data.VoteNum, data.Title, nil
}

func vote(token string, postID int64, direction int8) (bool, error) {
	var r apiResp
	_, err := call("POST", "/vote", token, map[string]string{
		"post_id":   strconv.FormatInt(postID, 10),
		"direction": strconv.FormatInt(int64(direction), 10),
	}, &r)
	if err != nil {
		return false, err
	}
	if r.Code != 1000 {
		return false, fmt.Errorf("code=%d msg=%s", r.Code, r.Msg)
	}
	var data struct {
		Changed bool `json:"changed"`
	}
	_ = json.Unmarshal(r.Data, &data)
	return data.Changed, nil
}

func updatePost(token string, postID int64, title string) error {
	var r apiResp
	_, err := call("PUT", "/post/"+strconv.FormatInt(postID, 10), token, map[string]string{
		"title": title, "content": "updated content",
	}, &r)
	if err != nil {
		return err
	}
	if r.Code != 1000 {
		return fmt.Errorf("code=%d msg=%s", r.Code, r.Msg)
	}
	return nil
}

func main() {
	stamp := time.Now().UnixNano()
	fmt.Println("=== E2E: 社区论坛后端 ===")

	// --- 1. 注册登录 + 建帖 ---
	token, uid, err := signupLogin(fmt.Sprintf("e2e_%d", stamp))
	must(err)
	fmt.Printf("[1] 注册登录 OK user_id=%d\n", uid)

	postID, err := createPost(token)
	must(err)
	fmt.Printf("[2] 建帖 OK post_id=%d\n", postID)

	// --- 2. 幂等：同一用户同方向重复投票 ---
	changed1, err := vote(token, postID, 1)
	must(err)
	changed2, err := vote(token, postID, 1)
	must(err)
	fmt.Printf("[3] 幂等: 第一次 changed=%v, 重复投票 changed=%v (期望 true/false)\n", changed1, changed2)

	// --- 3. 切换方向 ---
	changed3, err := vote(token, postID, -1)
	must(err)
	fmt.Printf("[4] 改投反对 changed=%v (期望 true)\n", changed3)
	_, err = vote(token, postID, 1)
	must(err)

	// --- 4. 并发投票：200 个不同用户 ---
	const voters = 200
	tokens := make([]string, voters)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 50)
	var okCount, errCount int64
	start := time.Now()
	for i := 0; i < voters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tk, _, err := signupLogin(fmt.Sprintf("e2e_%d_v%d", stamp, i))
			if err != nil {
				atomic.AddInt64(&errCount, 1)
				return
			}
			tokens[i] = tk
			if _, err := vote(tk, postID, 1); err != nil {
				atomic.AddInt64(&errCount, 1)
				return
			}
			atomic.AddInt64(&okCount, 1)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("[5] 并发投票 %d 用户: 成功=%d 失败=%d 耗时=%v\n", voters, okCount, errCount, elapsed.Round(time.Millisecond))

	time.Sleep(3 * time.Second) // 等待 MQ relay + consumer 落库

	// --- 5. 通过 HTTP 读回票数 ---
	voteNum, _, err := postDetail(postID)
	must(err)
	fmt.Printf("[6] HTTP 读帖子详情 vote_num=%d (期望 %d)\n", voteNum, okCount+1)

	// --- 6. 缓存一致性：先读一次建立缓存，再更新，再读 ---
	_, oldTitle, err := postDetail(postID)
	must(err)
	newTitle := fmt.Sprintf("updated-%d", stamp)
	must(updatePost(token, postID, newTitle))
	_, afterTitle, err := postDetail(postID + 1000)
	_ = afterTitle
	_, titleNow, err := postDetail(postID)
	must(err)
	fmt.Printf("[7] 缓存一致性: 旧标题=%q 更新后读到=%q 期望=%q -> %v\n",
		oldTitle, titleNow, newTitle, titleNow == newTitle)

	// --- 7. 刷票探测：单用户高频交替投票，看是否被限流 ---
	var burstOK, burstFail int64
	burstStart := time.Now()
	for i := 0; i < 100; i++ {
		direction := int8(1)
		if i%2 == 1 {
			direction = -1
		}
		if _, err := vote(token, postID, direction); err != nil {
			atomic.AddInt64(&burstFail, 1)
		} else {
			atomic.AddInt64(&burstOK, 1)
		}
	}
	fmt.Printf("[8] 单用户连发 100 次交替投票: 被受理=%d 被拒=%d 耗时=%v\n",
		burstOK, burstFail, time.Since(burstStart).Round(time.Millisecond))

	// --- 8. 单设备登录 ---
	tokenA, _, err := signupLogin(fmt.Sprintf("e2e_%d_sd", stamp))
	must(err)
	tokenB, _, err := signupLogin(fmt.Sprintf("e2e_%d_sd", stamp))
	must(err)
	var r apiResp
	_, _ = call("POST", "/vote", tokenA, map[string]string{
		"post_id": strconv.FormatInt(postID, 10), "direction": "1",
	}, &r)
	fmt.Printf("[9] 单设备登录: 旧 token code=%d (1000=有效, 非1000=已失效) 新 token!=旧 token=%v\n",
		r.Code, tokenA != tokenB)

	fmt.Printf("POST_ID=%d\n", postID)
	fmt.Printf("EXPECTED_VOTES=%d\n", okCount+1)
	_ = tokens
}

func must(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}
