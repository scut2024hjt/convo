import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';

const API = '/api/v1';
const base = '/app';
const AUTH_ERROR_CODES = new Set([1006, 1007]);

type Session = { user_id: string; username: string; token: string };
type Envelope<T> = { code: number; msg: string | Record<string,string>; data: T };
type Community = { id: string | number; name: string };
type Post = {
  id: string; author_id_str: string; author_name: string; title: string;
  content?: string; create_time: string; vote_num?: number; community: Community;
};
type FeedItem = {
  id: string; author_id: string; author_name: string; title: string;
  create_time: string; community: { id: string; name: string };
};
type FeedPage = { items: FeedItem[]; next_cursor: string | null };
type ApiFn = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
type Navigate = (route: string) => void;

function getSession(): Session | null {
  try {
    const stored = localStorage.getItem('convo.session');
    if (!stored) return null;
    const parsed = JSON.parse(stored) as Session;
    return typeof parsed.user_id === 'string' && typeof parsed.token === 'string' ? parsed : null;
  } catch { return null; }
}

function errorText(message: string | Record<string,string> | undefined): string {
  if (typeof message === 'string') return message;
  if (message && typeof message === 'object') return Object.values(message).join('；');
  return '请求失败，请稍后重试';
}

function timeLabel(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '时间未知';
  return date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

function routeFromPath(): string {
  const path = window.location.pathname;
  return path.startsWith(base + '/') ? path.slice(base.length) : '/';
}

function NavigateButton({ to, go, children, className = '' }: { to: string; go: Navigate; children: ReactNode; className?: string }) {
  return <button type="button" className={className} onClick={() => go(to)}>{children}</button>;
}

function Notice({ text, onDismiss }: { text: string; onDismiss: () => void }) {
  return <div className="notice" role="alert"><span>{text}</span><button onClick={onDismiss} aria-label="关闭提示">×</button></div>;
}

function FollowButton({ authorId, session, api, go, onChanged }: {
  authorId: string; session: Session | null; api: ApiFn; go: Navigate; onChanged: () => void;
}) {
  const [following, setFollowing] = useState(false);
  const [pending, setPending] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!session || !authorId || session.user_id === authorId) { setLoading(false); return; }
    let active = true;
    setLoading(true); setError('');
    api<{ following: boolean }>(`/users/${encodeURIComponent(authorId)}/follow`)
      .then(data => { if (active) setFollowing(data.following); })
      .catch(e => { if (active) setError((e as Error).message); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [authorId, session?.token, session?.user_id, api]);
  if (session?.user_id === authorId) return null;
  if (!session) return <button className="follow secondary" onClick={() => go('/login')}>登录后关注</button>;
  async function change() {
    if (pending || loading) return;
    setPending(true); setError('');
    try {
      const next = await api<{ following: boolean }>(`/users/${encodeURIComponent(authorId)}/follow`, following ? 'DELETE' : 'PUT');
      setFollowing(next.following);
      onChanged();
    } catch (e) { setError((e as Error).message); }
    finally { setPending(false); }
  }
  return <span className="follow-wrap">
    <button className={`follow ${following ? 'secondary' : 'primary'}`} disabled={pending || loading} onClick={change}>
      {loading ? '读取状态…' : pending ? '处理中…' : following ? '✓ 已关注' : '+ 关注作者'}
    </button>
    {error && <span className="inline-error" role="alert">{error}</span>}
  </span>;
}

function PostCard({ post, go, index }: { post: Post | FeedItem; go: Navigate; index?: number }) {
  return <article className="post-card">
    <button className="post-open" onClick={() => go(`/post/${encodeURIComponent(post.id)}`)}>
      <span className="post-meta"><span className="chip">{post.community?.name ?? '社区'}</span><span>{post.author_name}</span><span className="dot">·</span><span>{timeLabel(post.create_time)}</span></span>
      <span className="post-title">{post.title}</span>
      <span className="post-foot"><span>阅读讨论 <span aria-hidden="true">↗</span></span><span>{index ? `#${index} 热门讨论` : '查看详情'}</span></span>
    </button>
  </article>;
}

function PostList({ mode, api, go, session, refresh }: { mode: 'latest'|'hot'; api: ApiFn; go: Navigate; session: Session|null; refresh: number }) {
  const [posts, setPosts] = useState<Post[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    setLoading(true); setError('');
    api<Post[]>(`/posts2?page=1&size=20&order=${mode === 'hot' ? 'score' : 'Time'}`)
      .then(data => { if (active) setPosts(Array.isArray(data) ? data : []); })
      .catch(e => { if (active) setError((e as Error).message); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, mode, refresh]);
  return <div>
    <div className="section-heading"><div><span className="eyebrow">DISCOVER / 发现</span><h2>{mode === 'hot' ? '本周热门讨论' : '最新社区动态'}</h2><p>从一个好问题，开始新的交流。</p></div>
      {session && <NavigateButton to="/new" go={go} className="button primary">＋ 发起讨论</NavigateButton>}</div>
    {loading ? <div className="empty-state">正在加载讨论…</div> : error ? <div className="empty-state danger">{error}<button onClick={() => window.location.reload()}>重试</button></div> : posts.length === 0 ? <div className="empty-state">目前还没有帖子。{session ? '去发布第一篇吧。' : '登录后可以参与讨论。'}</div> :
    <div className="post-list">{posts.map((post,i) => <PostCard key={post.id} post={post} go={go} index={mode === 'hot' ? i + 1 : undefined} />)}</div>}
  </div>;
}

function Feed({ api, go, refresh }: { api: ApiFn; go: Navigate; refresh: number }) {
  const [items, setItems] = useState<FeedItem[]>([]);
  const [next, setNext] = useState<string|null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const inFlight = useRef(false);
  const cursorSeen = useRef(new Set<string>());
  const generation = useRef(0);

  const load = useCallback(async (cursor?: string) => {
    if (inFlight.current || (cursor && cursorSeen.current.has(cursor))) return;
    inFlight.current = true;
    setLoading(true); setError('');
    const token = generation.current;
    if (cursor) cursorSeen.current.add(cursor);
    try {
      const page = await api<FeedPage>(`/feed?limit=20${cursor ? '&cursor=' + encodeURIComponent(cursor) : ''}`);
      if (token !== generation.current) return;
      setItems(old => {
        const unique = new Map((cursor ? old : []).map(p => [p.id, p]));
        for (const p of page.items ?? []) unique.set(p.id, p);
        return [...unique.values()];
      });
      setNext(page.next_cursor ?? null);
    } catch (e) {
      if (token === generation.current) {
        if (cursor) cursorSeen.current.delete(cursor); // allow manual retry
        setError((e as Error).message);
      }
    } finally {
      if (token === generation.current) { inFlight.current = false; setLoading(false); }
    }
  }, [api]);

  useEffect(() => {
    generation.current += 1;
    inFlight.current = false;
    cursorSeen.current.clear(); setItems([]); setNext(null); setError('');
    void load();
    return () => { generation.current += 1; };
  }, [load, refresh]);

  return <div><div className="section-heading"><div><span className="eyebrow">YOUR CIRCLE / 关注</span><h2>你的关注流</h2><p>只看你关注的作者。新关注也会展示他们以前发布的帖子。</p></div>
    <button className="button subtle" onClick={() => { generation.current++; inFlight.current=false; cursorSeen.current.clear(); setItems([]); setNext(null); void load(); }}>↻ 刷新</button></div>
    {items.length > 0 && <div className="post-list">{items.map(p => <PostCard key={p.id} post={p} go={go} />)}</div>}
    {items.length === 0 && !loading && !error && <div className="empty-state"><span className="empty-glyph">◎</span><h3>关注的人，还没有动态</h3><p>去首页发现帖子，再从详情页关注感兴趣的作者。</p><NavigateButton className="button secondary" to="/" go={go}>探索帖子</NavigateButton></div>}
    {error && <div className="empty-state danger" role="alert">{error}<button onClick={() => void load(next ?? undefined)}>重试</button></div>}
    {loading && <div className="load-line">正在读取时间线…</div>}
    {next && !loading && <div className="load-line"><button className="button secondary" onClick={() => void load(next)}>加载更多</button></div>}
  </div>;
}

function PostDetail({ postId, api, go, session, onFollowChange }: {
  postId: string; api: ApiFn; go: Navigate; session: Session|null; onFollowChange: () => void;
}) {
  const [post, setPost] = useState<Post|null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [votePending, setVotePending] = useState(false);
  const [voteError, setVoteError] = useState('');
  const [direction, setDirection] = useState<'1'|'-1'|'0'|'?'>('?');
  const refresh = useCallback(() => {
    let active = true;
    setLoading(true); setError('');
    api<Post>(`/post/${encodeURIComponent(postId)}`)
      .then(data => { if (active) setPost(data); })
      .catch(e => { if (active) setError((e as Error).message); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, postId]);
  useEffect(() => refresh(), [refresh]);
  async function vote(value: '1'|'-1'|'0') {
    if (!session) { go('/login'); return; }
    if (votePending) return;
    setVoteError(''); setVotePending(true);
    try {
      await api('/vote', 'POST', { post_id: postId, direction: value });
      setDirection(value);
      // The vote score is read from Redis; do not optimistically fake a count.
      try {
        setPost(await api<Post>(`/post/${encodeURIComponent(postId)}`));
      } catch (refreshError) {
        setVoteError(`投票已提交，但刷新票数失败：${(refreshError as Error).message}`);
      }
    } catch(e) { setVoteError((e as Error).message); }
    finally { setVotePending(false); }
  }
  return <div className="detail-content"><button className="back-link" onClick={() => go('/')}>← 返回讨论</button>
    {loading ? <div className="empty-state">正在加载帖子…</div> : error || !post ? <div className="empty-state danger">{error || '帖子不存在'}<button onClick={() => { const cleanup = refresh(); void cleanup; }}>重新读取</button></div> : <article className="detail-card">
      <div className="meta-line"><span className="chip">{post.community?.name}</span><span>{timeLabel(post.create_time)}</span></div>
      <h1>{post.title}</h1>
      <div className="author-row"><div className="avatar">{post.author_name?.slice(0,1).toUpperCase()}</div><div><strong>{post.author_name}</strong><small>社区成员 · 作者</small></div>
        <div className="author-action"><FollowButton authorId={post.author_id_str} session={session} api={api} go={go} onChanged={onFollowChange} /></div></div>
      <div className="post-body">{post.content}</div>
      <div className="vote-row"><div><strong>{post.vote_num ?? 0}</strong><small>当前赞成票</small></div><div className="vote-actions">
        <button disabled={votePending} className={direction === '1' ? 'active' : ''} onClick={() => void vote('1')}>↑ 赞同</button>
        <button disabled={votePending} className={direction === '-1' ? 'active' : ''} onClick={() => void vote('-1')}>↓ 反对</button>
        <button disabled={votePending} onClick={() => void vote('0')}>撤销</button>
      </div></div>
      {voteError && <div className="inline-error" role="alert">{voteError.includes('索引正在同步') ? voteError + '。请稍后手动重试，系统不会自动重复提交投票。' : '投票失败：' + voteError + '（可以重试）'}</div>}
      {direction === '?' && <p className="hint">后端当前未提供我的投票方向查询接口；刷新页面不会假设已投方向。</p>}
    </article>}
  </div>;
}

function AuthForm({ register, api, setSession, go }: { register: boolean; api: ApiFn; setSession: (value: Session)=>void; go: Navigate }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [repeat, setRepeat] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  async function submit(event: FormEvent) {
    event.preventDefault(); setError('');
    if (username.trim().length < 3 || username.trim().length > 32) { setError('用户名需要 3–32 个字符'); return; }
    if (password.length < 8 || password.length > 72) { setError('密码需要 8–72 个字符'); return; }
    if (register && password !== repeat) { setError('两次输入的密码不一致'); return; }
    setPending(true);
    try {
      if (register) { await api('/signup', 'POST', { username, password, re_password: repeat }); go('/login'); }
      else { const data = await api<Session>('/login', 'POST', { username, password }); setSession(data); go('/feed'); }
    } catch (e) { setError((e as Error).message); }
    finally { setPending(false); }
  }
  return <div className="auth-layout"><div className="auth-art"><span className="eyebrow">WELCOME TO CONVO</span><h1>好问题，<br/>值得被看见。</h1><p>用你的观点连接更多人。探索不同的想法，也分享自己的故事。</p><div className="quote-mark">“</div></div>
    <div className="auth-form"><span className="eyebrow">ACCOUNT / 账户</span><h2>{register ? '创建账号' : '欢迎回来'}</h2><p>{register ? '加入社区，开启你的第一场讨论。' : '登录后关注作者、发帖和参与投票。'}</p>
      <form onSubmit={e => void submit(e)}><label>用户名<input autoComplete="username" value={username} onChange={e => setUsername(e.target.value)} placeholder="请输入用户名" required /></label>
        <label>密码<input type="password" autoComplete={register ? 'new-password' : 'current-password'} value={password} onChange={e => setPassword(e.target.value)} placeholder="至少 8 个字符" required /></label>
        {register && <label>确认密码<input type="password" autoComplete="new-password" value={repeat} onChange={e => setRepeat(e.target.value)} required /></label>}
        {error && <p className="form-error" role="alert">{error}</p>}
        <button type="submit" className="button primary full" disabled={pending}>{pending ? '提交中…' : register ? '注册账号' : '登录'}</button></form>
      <p className="swap-auth">{register ? '已有账号？' : '还没有账号？'} <button onClick={() => go(register ? '/login' : '/register')}>{register ? '去登录' : '立即注册'} →</button></p>
    </div></div>;
}

function NewPost({ api, go, onPublished }: { api: ApiFn; go: Navigate; onPublished: (id: string) => void }) {
  const [communities, setCommunities] = useState<Community[]>([]);
  const [communityId, setCommunityId] = useState('');
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  useEffect(() => {
    let active = true;
    api<Community[]>('/community').then(data => { if (active) { setCommunities(data ?? []); setCommunityId(String(data?.[0]?.id ?? '')); } }).catch(e => { if (active) setError((e as Error).message); });
    return () => { active = false; };
  }, [api]);
  async function submit(e: FormEvent) {
    e.preventDefault(); setError('');
    if (!communityId || title.trim().length === 0 || title.length > 128 || content.trim().length === 0 || content.length > 8192) {
      setError('请选择社区，标题不超过 128 字，正文不超过 8192 字'); return;
    }
    setPending(true);
    try {
      const result = await api<{ post_id: string; distribution?: string }>('/post', 'POST', { community_id: Number(communityId), title, content });
      // Do not resubmit on delayed projections: this response means the DB
      // transaction committed, not that Redis/Feed projections are ready.
      if (result.distribution === 'async') onPublished(result.post_id);
      go(`/post/${encodeURIComponent(result.post_id)}`);
    } catch (err) { setError((err as Error).message); }
    finally { setPending(false); }
  }
  return <div className="composer"><span className="eyebrow">CREATE / 分享</span><h1>发起一场讨论</h1><p>分享想法、提出问题，也许有人正好需要看到它。</p>
    <form onSubmit={e => void submit(e)}><label>选择社区<select value={communityId} onChange={e => setCommunityId(e.target.value)} required><option value="">选择社区</option>{communities.map(c => <option key={String(c.id)} value={String(c.id)}>{c.name}</option>)}</select></label>
      <label>标题<input value={title} maxLength={128} onChange={e => setTitle(e.target.value)} placeholder="一句话说明你想讨论什么" required /></label>
      <label>正文<textarea rows={12} maxLength={8192} value={content} onChange={e => setContent(e.target.value)} placeholder="把你的思考写下来…" required /></label>
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="compose-action"><span>{content.length} / 8192 字</span><button type="submit" className="button primary" disabled={pending}>{pending ? '发布中…' : '发布帖子 →'}</button></div>
    </form></div>;
}

export default function App() {
  const [session, saveSession] = useState<Session|null>(getSession);
  const [route, setRoute] = useState(routeFromPath);
  const [notice, setNotice] = useState('');
  const [refresh, setRefresh] = useState(0);
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const [authEpoch, setAuthEpoch] = useState(0);
  const setSession = useCallback((value: Session | null) => {
    if (value) localStorage.setItem('convo.session', JSON.stringify(value));
    else localStorage.removeItem('convo.session');
    saveSession(value); setAuthEpoch(n => n + 1); setRefresh(n => n + 1);
  }, []);

  const go = useCallback((to: string) => {
    window.history.pushState({}, '', base + to);
    setRoute(to);
    window.scrollTo({ top: 0, behavior: 'auto' });
  }, []);
  useEffect(() => {
    const pop = () => setRoute(routeFromPath());
    window.addEventListener('popstate', pop);
    return () => window.removeEventListener('popstate', pop);
  }, []);

  // The session is read at call time; stale requests don't reuse an old user.
  const api: ApiFn = useCallback(async <T,>(path: string, method='GET', body?: unknown): Promise<T> => {
    const token = sessionRef.current?.token;
    let response: Response;
    try {
      response = await fetch(API + path, {
        method, headers: { ...(body ? { 'Content-Type': 'application/json' } : {}), ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch { throw new Error('网络连接失败，请检查后端服务并重试'); }
    if (!response.ok) throw new Error(`HTTP ${response.status}：请求未成功`);
    let envelope: Envelope<T>;
    try { envelope = await response.json() as Envelope<T>; }
    catch { throw new Error('服务器返回的数据格式无效'); }
    if (envelope.code !== 1000) {
      if (AUTH_ERROR_CODES.has(envelope.code) && token) {
        // Identity changed or expired: clear all UI and follow/feed state.
        if (sessionRef.current?.token === token) {
          setSession(null); setNotice('登录状态已失效，请重新登录');
        }
      }
      throw new Error(errorText(envelope.msg));
    }
    return envelope.data;
  }, [setSession]);

  const isPost = /^\/post\/[1-9][0-9]*$/.test(route);
  const needsSession = route === '/feed' || route === '/new';
  async function logout() {
    try { await api('/logout', 'POST'); }
    catch (e) { setNotice(`已清理本地会话；服务端登出未确认：${(e as Error).message}`); }
    finally { setSession(null); go('/'); }
  }
  const onFollowChange = useCallback(() => setRefresh(n => n + 1), []);

  return <div className="site">
    <header className="topbar"><div className="top-inner"><button className="wordmark" onClick={() => go('/')}>convo<span>.</span></button>
      <nav aria-label="主导航"><NavigateButton to="/" go={go} className={route === '/' ? 'nav active' : 'nav'}>最新</NavigateButton>
        <NavigateButton to="/hot" go={go} className={route === '/hot' ? 'nav active' : 'nav'}>热门</NavigateButton>
        {session && <NavigateButton to="/feed" go={go} className={route === '/feed' ? 'nav active' : 'nav'}>关注</NavigateButton>}</nav>
      <div className="top-actions">{session ? <><span className="user-chip"><span className="user-dot" />{session.username}</span><NavigateButton go={go} to="/new" className="button small primary">写帖子</NavigateButton><button className="text-button" onClick={() => void logout()}>退出</button></> : <><NavigateButton go={go} to="/login" className="text-button">登录</NavigateButton><NavigateButton go={go} to="/register" className="button small primary">加入社区</NavigateButton></>}</div>
    </div></header>
    {notice && <Notice text={notice} onDismiss={() => setNotice('')} />}
    <main key={`${authEpoch}:${route}`} className="main">
      {(route === '/login' || route === '/register') ? <AuthForm register={route === '/register'} api={api} setSession={setSession} go={go} /> :
      needsSession && !session ? <div className="empty-state"><h2>需要登录才能继续</h2><NavigateButton go={go} to="/login" className="button primary">去登录</NavigateButton></div> :
      route === '/new' ? <NewPost api={api} go={go} onPublished={() => setNotice('帖子已保存。内容索引和关注分发正在后台处理，首页与关注流可能稍后才出现；现在投票若提示同步中，请稍后手动重试。')} /> :
      isPost ? <PostDetail postId={route.split('/')[2]} api={api} go={go} session={session} onFollowChange={onFollowChange} /> :
      route === '/feed' ? <Feed api={api} go={go} refresh={refresh} /> :
      route === '/hot' || route === '/' ? <div className="home-grid"><section><div className="hero"><span className="eyebrow">AN OPEN SPACE FOR IDEAS</span><h1>每一个想法，<br/><em>都值得交流。</em></h1><p>发现值得思考的讨论，和好奇的人一起拓展视野。</p></div><PostList mode={route === '/hot' ? 'hot' : 'latest'} api={api} go={go} session={session} refresh={refresh} /></section>
        <aside className="sidebar"><div className="side-card"><span className="eyebrow">WELCOME / CONVO</span><h3>因为好奇，<br/>我们聚在这里。</h3><p>向世界提问，分享你的思考，让讨论成为连接。</p>{session ? <NavigateButton to="/feed" go={go} className="button outline full">浏览我的关注 →</NavigateButton> : <NavigateButton to="/register" go={go} className="button outline full">开始参与 →</NavigateButton>}</div><div className="side-note"><span>01 / 发现</span><p>浏览新帖与热榜，找到感兴趣的话题。</p><span>02 / 关注</span><p>关注作者，创建属于自己的时间线。</p><span>03 / 参与</span><p>投票、发帖，留下你的声音。</p></div></aside></div> :
      <div className="empty-state"><h2>找不到页面</h2><NavigateButton to="/" go={go} className="button secondary">返回首页</NavigateButton></div>}
    </main>
    <footer className="footer"><span>convo<span className="accent">.</span></span><span>开放讨论 · 有序交流</span><span>© 2026 Convo</span></footer>
  </div>;
}
