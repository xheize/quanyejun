"use client";

import { FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { connectRuntime, type ConnectionState } from "./runtime-connection";

type Message = { expiresAt?: string; id: string; role: "user" | "assistant"; content: string; createdAt: string };
type RuntimeTask = { expiresAt?: string; id: string; title: string; status: string; progress: number; result?: string; createdAt: string; updatedAt: string };
type AgentRun = { id: string; status: string; goal: string; result?: string; error?: string; createdAt: string; updatedAt: string };
type Memory = { expiresAt?: string; id: string; content: string; createdAt: string };
type RuntimeEvent = { expiresAt?: string; id: string; type: string; timestamp: string; payload: Record<string, unknown> };
type ProviderInfo = { id: string; label: string; model?: string; source: string };
type EnvironmentInfo = {
  kind: string;
  profile: string;
  os: string;
  arch: string;
  capabilities: string[];
  authority: { mode: string; granted: string[] };
  tools: { name: string; path?: string; available: boolean }[];
};
type VisitorSession = { id: string; role: "anonymous" | "authenticated"; loginEnabled: boolean; displayName: string; displayNameExpiresAt?: string };
type Bootstrap = {
  session: VisitorSession;
  identity: { id: string; name: string; status: string };
  environment: EnvironmentInfo;
  provider: ProviderInfo;
  messages: Message[];
  memories: Memory[];
  tasks: RuntimeTask[];
  runs: AgentRun[];
  events: RuntimeEvent[];
};


const apiBase = (process.env.NEXT_PUBLIC_RUNTIME_URL || "").trim().replace(/\/+$/, "");
const seedMessages: Message[] = [
  { id: "seed-1", role: "assistant", content: "좋은 아침이에요. 하나의 지속되는 Identity로 준비되어 있어요. 런타임을 연결하면 이 대화와 작업 상태가 PostgreSQL에 이어서 저장됩니다.", createdAt: "" },
];
const previewEvent: RuntimeEvent = { id: "preview", type: "preview", timestamp: "", payload: {} };
const previewProvider: ProviderInfo = { id: "builtin", label: "Built-in preview", source: "fallback" };
const previewEnvironment: EnvironmentInfo = { kind: "local", profile: "preview", os: "unknown", arch: "unknown", capabilities: [], authority: { mode: "internal-only", granted: [] }, tools: [] };

const eventCopy: Record<string, [string, string]> = {
  "preview": ["미리보기", "서비스 연결 후 실제 활동이 여기에 표시됩니다."],
  "system.ready": ["런타임 준비", "지속 Identity가 입력을 받을 준비를 마쳤습니다."],
  "chat.message": ["새 대화", "대화가 이벤트와 기억 후보로 기록됐습니다."],
  "run.created": ["추론 작업 생성", "요청을 영속 Agent Run으로 등록했습니다."],
  "run.started": ["추론 시작", "API 모델과 도구 실행 루프를 시작했습니다."],
  "run.completed": ["추론 완료", "최종 응답과 실행 단계를 저장했습니다."],
  "run.failed": ["추론 실패", "실행 기록을 보존한 채 작업을 종료했습니다."],
  "tool.started": ["도구 실행", "권한이 허용된 런타임 도구를 실행하고 있습니다."],
  "tool.completed": ["도구 완료", "도구 결과를 모델 문맥으로 반환했습니다."],
  "task.created": ["작업 생성", "WebSocket 연결과 독립된 작업을 시작했습니다."],
  "task.progress": ["작업 진행", "결정적 런타임이 진행 상태를 갱신했습니다."],
  "task.completed": ["작업 완료", "결과를 저장하고 모든 연결에 전달했습니다."],
};

function upsert<T extends { id: string }>(items: T[], item: T) {
  const index = items.findIndex((current) => current.id === item.id);
  if (index === -1) return [...items, item];
  return items.map((current, currentIndex) => currentIndex === index ? item : current);
}

function shortTime(value: string) {
  if (!value) return "방금";
  return new Intl.DateTimeFormat("ko-KR", { hour: "numeric", minute: "2-digit" }).format(new Date(value));
}

function relativeTime(value: string) {
  if (!value) return "방금";
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 15) return "방금";
  if (seconds < 60) return `${seconds}초 전`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}분 전`;
  return `${Math.floor(seconds / 3600)}시간 전`;
}

export function Dashboard() {
  const [session, setSession] = useState<VisitorSession | null>(null);
  const [aliasDraft, setAliasDraft] = useState("");
  const [savingAlias, setSavingAlias] = useState(false);
  const [identityName, setIdentityName] = useState("QuanYejun");
  const [messages, setMessages] = useState<Message[]>(seedMessages);
  const [tasks, setTasks] = useState<RuntimeTask[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [provider, setProvider] = useState<ProviderInfo>(previewProvider);
  const [environment, setEnvironment] = useState<EnvironmentInfo>(previewEnvironment);
  const [connection, setConnection] = useState<ConnectionState>(apiBase ? "connecting" : "offline");
  const [retriesExhausted, setRetriesExhausted] = useState(false);
  const runtimeReady = Boolean(apiBase) && connection === "online" && Boolean(session?.id);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [notice, setNotice] = useState("");
  const lastEventId = useRef("");
  const scrollAnchor = useRef<HTMLDivElement>(null);

  const applyEvent = useCallback((event: RuntimeEvent) => {
    lastEventId.current = event.id;
    setEvents((current) => upsert(current, event).slice(-20));
    const message = event.payload.message as Message | undefined;
    const task = event.payload.task as RuntimeTask | undefined;
    if (message?.id) {
      setMessages((current) => upsert(current, message));
      if (message.role === "assistant") setSending(false);
    }
    if (task?.id) setTasks((current) => upsert(current, task));
    if (event.type === "run.failed") {
      setSending(false);
      setNotice("추론 작업이 실패했습니다. 활동 스트림에서 실행 기록을 확인해주세요.");
    }
  }, []);

  useEffect(() => connectRuntime({
    apiBase,
    onBootstrap: async (response, signal) => {
      if (response.status === 401) {
        setMessages([]); setMemories([]); setTasks([]); setEvents([]);
        lastEventId.current = "";
        setSession({ id: "", role: "anonymous", loginEnabled: true, displayName: "방문자님" });
        setNotice("로그인 세션을 확인할 수 없습니다. 다시 로그인해주세요.");
      }
      if (!response.ok) throw new Error("runtime unavailable");
      const data = await response.json() as Bootstrap;
      if (signal.aborted) return;
      setSession(data.session);
      setIdentityName(data.identity?.name || "QuanYejun");
      setMessages(data.messages.length ? data.messages : seedMessages);
      setTasks(data.tasks || []);
      setMemories(data.memories || []);
      setEvents(data.events || []);
      setProvider(data.provider || previewProvider);
      setEnvironment(data.environment || previewEnvironment);
      lastEventId.current = data.events.at(-1)?.id || "";
    },
    cursor: () => lastEventId.current,
    onMessage: (data) => {
      try { applyEvent(JSON.parse(data) as RuntimeEvent); } catch { /* ignore malformed external events */ }
    },
    onState: setConnection,
    onExhausted: () => setRetriesExhausted(true),
  }), [applyEvent]);

  useEffect(() => {
    scrollAnchor.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, sending]);

  async function sendMessage(event: FormEvent) {
    event.preventDefault();
    const content = draft.trim();
    if (!runtimeReady || !content || sending) return;
    setDraft("");
    setSending(true);
    setNotice("");
    try {
      const response = await fetch(`${apiBase}/api/messages`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Requested-With": "QuanYejun" },
        body: JSON.stringify({ content }),
      });
      if (!response.ok) throw new Error("message failed");
      const result = await response.json() as { userMessage: Message; runId: string; run: AgentRun };
      setMessages((current) => upsert(current, result.userMessage));
    } catch {
      setDraft(content);
      setNotice("런타임에 연결할 수 없습니다. 잠시 뒤 다시 시도해주세요.");
      setSending(false);
    }
  }

  function handleComposerKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      event.currentTarget.form?.requestSubmit();
    }
  }

  async function startDemoTask() {
    if (!runtimeReady) return;
    setNotice("");
    try {
      const response = await fetch(`${apiBase}/api/tasks/demo`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", "X-Requested-With": "QuanYejun" },
        body: JSON.stringify({ durationSeconds: 12 }),
      });
      if (!response.ok) throw new Error("task failed");
      const task = await response.json() as RuntimeTask;
      setTasks((current) => upsert(current, task));
    } catch {
      setNotice("작업을 시작하려면 Go 런타임을 먼저 실행해주세요.");
    }
  }

  useEffect(() => {
    const timer = setInterval(() => {
      const live = (item: { expiresAt?: string }) => !item.expiresAt || Date.parse(item.expiresAt) > Date.now();
      setMessages(items => items.filter(live));
      setMemories(items => items.filter(live));
      setTasks(items => items.filter(live));
      setEvents(items => items.filter(live));
      setSession(current => current?.displayNameExpiresAt && Date.parse(current.displayNameExpiresAt) <= Date.now()
        ? { ...current, displayName: "방문자님", displayNameExpiresAt: undefined } : current);
    }, 1000);
    return () => clearInterval(timer);
  }, []);

  async function logout() {
    if (!runtimeReady) return;
    try {
      const response = await fetch(`${apiBase}/auth/logout`, { method: "POST", credentials: "include", headers: { "X-Requested-With": "QuanYejun" } });
      if (!response.ok) throw new Error("logout failed");
      window.location.reload();
    } catch { setNotice("로그아웃하지 못했습니다. 다시 시도해주세요."); }
  }

  async function saveAlias(event: FormEvent) {
    event.preventDefault();
    if (!runtimeReady || savingAlias) return;
    setSavingAlias(true);
    try {
      const response = await fetch(`${apiBase}/api/session/display-name`, {
        method: "POST", credentials: "include",
        headers: { "Content-Type": "application/json", "X-Requested-With": "QuanYejun" },
        body: JSON.stringify({ displayName: aliasDraft }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "별칭을 저장하지 못했습니다.");
      setSession(data as VisitorSession);
      setAliasDraft("");
      setNotice("별칭을 저장했습니다.");
    } catch (error) { setNotice(error instanceof Error ? error.message : "별칭을 저장하지 못했습니다."); }
    finally { setSavingAlias(false); }
  }

  const activeTask = [...tasks].reverse().find((task) => task.status === "running" || task.status === "queued");
  const latestTask = activeTask || tasks.at(-1);
  const latestMemory = memories.at(-1)?.content || "대화에서 오래 남길 가치가 있는 사실을 발견하면 여기에 기억합니다.";
  const visibleEvents = useMemo(() => [...events].reverse().slice(0, 4), [events]);
  const identityInitial = Array.from(identityName.trim())[0] || "Q";
  const availableTools = environment.tools.filter((tool) => tool.available).length;

  return (
    <main className="shell">
      <aside className="identity-panel">
        <div className="brand-mark" aria-hidden="true">Q</div>
        <div className="identity-copy">
          <p className="eyebrow">PERSISTENT IDENTITY</p><h1>{identityName}</h1>
          <p className="identity-state"><span className={`status-dot ${connection}`} /> {connection === "online" ? "깨어 있음" : connection === "connecting" ? "연결 중" : "런타임 대기"}</p>
        </div>
        <div className="session-controls">
          <p>현재 호칭: {session?.displayName || "방문자님"}</p>
          <form className="alias-form" onSubmit={saveAlias}>
            <label htmlFor="visitor-alias">대화용 별칭</label>
            <input id="visitor-alias" value={aliasDraft} onChange={event => setAliasDraft(event.target.value)} placeholder="새 별칭 (최대 40자)" aria-describedby="alias-help" disabled={!runtimeReady || savingAlias} />
            <button className="task-launch" type="submit" disabled={!runtimeReady || savingAlias}>{savingAlias ? "저장 중" : aliasDraft.trim() ? "별칭 저장" : "기본 호칭으로 초기화"}</button>
            <p id="alias-help">빈 값은 방문자님으로 초기화됩니다. 별칭은 로그인 상태나 권한을 바꾸지 않습니다.</p>
          </form>
          <p>{!session ? "미리보기 · 대화는 저장되지 않습니다" : session.role === "authenticated" ? "로그인됨 · 대화가 계속 이어집니다" : "익명 · 각 기록은 생성 후 72시간까지만 보관됩니다"}</p>
          {session?.role === "authenticated" ? <button className="task-launch" type="button" onClick={logout} disabled={!runtimeReady}>로그아웃</button> : session?.loginEnabled ? <a className="task-launch" href={`${apiBase}/auth/login`}>SSO 로그인</a> : <span>로그인 준비 중</span>}
          {session?.role !== "authenticated" && session?.loginEnabled && <p>로그인하면 회원 대화로 전환됩니다. 익명 기록은 이전되지 않습니다.</p>}
        </div>
        <nav className="primary-nav" aria-label="주요 메뉴">
          <a className="nav-item active" href="#conversation"><span>◉</span> 대화 <small>{messages.length}</small></a>
          <a className="nav-item" href="#activity"><span>↗</span> 활동</a>
          <a className="nav-item" href="#memory"><span>◇</span> 기억</a>
          <a className="nav-item" href="#tasks"><span>✓</span> 작업</a>
        </nav>
        <section className="identity-card" id="memory">
          <div className="section-heading"><span>현재 상태</span><span className="quiet">{connection === "online" ? "LIVE" : "LOCAL"}</span></div>
          <dl>
            <div><dt>기억</dt><dd>{memories.length}</dd></div>
            <div><dt>진행 작업</dt><dd>{tasks.filter((task) => task.status === "running").length}</dd></div>
            <div><dt>실행 환경</dt><dd>{environment.kind} · {environment.profile}</dd></div>
            <div><dt>마지막 활동</dt><dd>{events.length ? relativeTime(events.at(-1)!.timestamp) : "대기"}</dd></div>
          </dl>
        </section>
        <p className="model-label" title={provider.source}>Reasoning engine · {provider.label}{provider.model ? ` · ${provider.model}` : ""}</p>
        <p className="environment-label" title={`${environment.os}/${environment.arch} · authority ${environment.authority.mode}`}>Environment · {availableTools} tools</p>
      </aside>

      <section className="conversation" id="conversation">
        <header className="conversation-header">
          <div><p className="eyebrow">하나의 이어지는 대화</p><h2>{session?.displayName || "방문자님"}과 {identityName}</h2></div>
          <button className="task-launch" type="button" onClick={startDemoTask} disabled={!runtimeReady}>비동기 작업 실행</button>
        </header>
        <div className="conversation-body" aria-live="polite">
          {!runtimeReady && <p className="preview-notice" role="status">{!apiBase
            ? "미리보기입니다. 대화 전송과 작업 실행은 서비스 연결 후 사용할 수 있습니다."
            : retriesExhausted
              ? "연결 재시도 10회를 마쳐 자동 연결을 중단했습니다. 다시 연결하려면 페이지를 새로고침해주세요."
              : "서비스에 연결 중입니다. 연결이 완료되면 대화와 작업을 사용할 수 있습니다."}</p>}
          <div className="date-divider"><span>오늘</span></div>
          {messages.map((message) => (
            <article className={`message ${message.role === "user" ? "user-message" : "entity-message"}`} key={message.id}>
              {message.role === "assistant" && <div className="avatar">{identityInitial}</div>}
              <div>
                <div className="message-meta"><strong>{message.role === "user" ? "나" : identityName}</strong><time>{shortTime(message.createdAt)}</time></div>
                <div className="message-bubble"><p>{message.content}</p></div>
              </div>
            </article>
          ))}
          {latestTask && (
            <article className="task-card" id="tasks">
              <div className="task-topline">
                <span className="task-icon">↗</span>
                <div><p className="eyebrow">ASYNC TASK</p><h3>{latestTask.title}</h3></div>
                <span className={`task-state ${latestTask.status}`}>{latestTask.status === "completed" ? "완료" : latestTask.status === "failed" ? "실패" : "실행 중"}</span>
              </div>
              <div className="progress-track"><span style={{ width: `${latestTask.progress}%` }} /></div>
              <div className="task-footer"><span>{latestTask.result || "WebSocket과 독립적으로 실행 중"}</span><strong>{latestTask.progress}%</strong></div>
            </article>
          )}
          {sending && <article className="message entity-message latest"><div className="avatar">{identityInitial}</div><div><div className="message-meta"><strong>{identityName}</strong><time>생각 중</time></div><div className="message-bubble typing"><span /><span /><span /></div></div></article>}
          <div ref={scrollAnchor} />
        </div>
        <footer className="composer-wrap">
          {notice && <p className="runtime-notice" role="alert">{notice}</p>}
          <form className="composer" onSubmit={sendMessage}>
            <label className="sr-only" htmlFor="message">메시지</label>
            <textarea id="message" name="message" value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={handleComposerKey} placeholder={runtimeReady ? `${identityName}에게 메시지 보내기` : "서비스 연결 후 메시지를 보낼 수 있습니다"} disabled={!runtimeReady} rows={1} />
            <div className="composer-actions">
              <span className={`connection-chip ${connection}`}>{connection === "online" ? "실시간 연결" : !apiBase ? "미리보기" : "연결 대기"}</span>
              <span>Enter 전송 · Shift + Enter 줄바꿈</span>
              <button className="send-button" type="submit" aria-label="메시지 전송" disabled={!runtimeReady || !draft.trim() || sending}>↑</button>
            </div>
          </form>
          <p className="continuity-note">{runtimeReady ? "대화가 닫혀도 기억과 작업은 계속됩니다." : "현재 대화 전송과 작업 실행을 사용할 수 없습니다."}</p>
        </footer>
      </section>

      <aside className="activity-panel" id="activity">
        <header><div><p className="eyebrow">WORLD STATE</p><h2>지금 일어나는 일</h2></div><span className={`pulse-ring ${connection}`} aria-label={`연결 상태: ${connection}`} /></header>
        <section className="focus-card">
          <p className="eyebrow">CURRENT FOCUS</p>
          <h3>{activeTask ? activeTask.title : "연결이 끊겨도\n일은 계속되어야 한다."}</h3>
          <div className="focus-meta"><span>runtime</span><span>{activeTask ? `${activeTask.progress}%` : "websocket"}</span></div>
        </section>
        <section className="timeline">
          <div className="section-heading"><span>활동 스트림</span><span className="activity-count">{events.length}</span></div>
          {(visibleEvents.length ? visibleEvents : [previewEvent]).map((event) => {
            const copy = eventCopy[event.type] || [event.type, "정규화된 이벤트가 세계 상태에 반영됐습니다."];
            const tone = event.type.includes("completed") ? "done" : event.type.includes("chat") ? "memory" : "live";
            return <article className="timeline-item" key={event.id}><div className={`timeline-dot ${tone}`} /><div><time>{relativeTime(event.timestamp)}</time><h3>{copy[0]}</h3><p>{copy[1]}</p></div></article>;
          })}
        </section>
        <section className="memory-note"><div className="memory-glyph">◇</div><div><p className="eyebrow">RELEVANT MEMORY</p><p>“{latestMemory}”</p></div></section>
      </aside>
    </main>
  );
}
