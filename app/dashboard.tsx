"use client";

import { FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";

type Message = { id: string; role: "user" | "assistant"; content: string; createdAt: string };
type RuntimeTask = { id: string; title: string; status: string; progress: number; result?: string; createdAt: string; updatedAt: string };
type Memory = { id: string; content: string; createdAt: string };
type RuntimeEvent = { id: string; type: string; timestamp: string; payload: Record<string, unknown> };
type Bootstrap = {
  identity: { id: string; name: string; status: string };
  messages: Message[];
  memories: Memory[];
  tasks: RuntimeTask[];
  events: RuntimeEvent[];
};

const actorId = "person_example";
const apiBase = (process.env.NEXT_PUBLIC_RUNTIME_URL || "http://localhost:8080").replace(/\/$/, "");
const seedMessages: Message[] = [
  { id: "seed-1", role: "assistant", content: "좋은 아침이에요. 하나의 지속되는 Identity로 준비되어 있어요. 런타임을 연결하면 이 대화와 작업 상태가 PostgreSQL에 이어서 저장됩니다.", createdAt: "" },
];
const previewEvent: RuntimeEvent = { id: "preview", type: "system.ready", timestamp: "", payload: {} };

const eventCopy: Record<string, [string, string]> = {
  "system.ready": ["런타임 준비", "지속 Identity가 입력을 받을 준비를 마쳤습니다."],
  "chat.message": ["새 대화", "대화가 이벤트와 기억 후보로 기록됐습니다."],
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
  const [messages, setMessages] = useState<Message[]>(seedMessages);
  const [tasks, setTasks] = useState<RuntimeTask[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [connection, setConnection] = useState<"connecting" | "online" | "offline">("connecting");
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
    if (message?.id) setMessages((current) => upsert(current, message));
    if (task?.id) setTasks((current) => upsert(current, task));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let socket: WebSocket | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;

    async function hydrate() {
      try {
        const response = await fetch(`${apiBase}/api/bootstrap?actorId=${actorId}`);
        if (!response.ok) throw new Error("runtime unavailable");
        const data = await response.json() as Bootstrap;
        if (cancelled) return;
        setMessages(data.messages.length ? data.messages : seedMessages);
        setTasks(data.tasks || []);
        setMemories(data.memories || []);
        setEvents(data.events || []);
        lastEventId.current = data.events.at(-1)?.id || "";
        connect();
      } catch {
        if (!cancelled) {
          setConnection("offline");
          retry = setTimeout(hydrate, 2500);
        }
      }
    }

    function connect() {
      if (cancelled) return;
      setConnection("connecting");
      const wsBase = apiBase.replace(/^http/, "ws");
      const cursor = encodeURIComponent(lastEventId.current);
      socket = new WebSocket(`${wsBase}/ws?actorId=${actorId}&lastEventId=${cursor}`);
      socket.onopen = () => setConnection("online");
      socket.onmessage = (message) => {
        try { applyEvent(JSON.parse(message.data) as RuntimeEvent); } catch { /* ignore malformed external events */ }
      };
      socket.onclose = () => {
        if (!cancelled) {
          setConnection("offline");
          retry = setTimeout(connect, 1800);
        }
      };
      socket.onerror = () => socket?.close();
    }

    hydrate();
    return () => {
      cancelled = true;
      if (retry) clearTimeout(retry);
      socket?.close();
    };
  }, [applyEvent]);

  useEffect(() => {
    scrollAnchor.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, sending]);

  async function sendMessage(event: FormEvent) {
    event.preventDefault();
    const content = draft.trim();
    if (!content || sending) return;
    setDraft("");
    setSending(true);
    setNotice("");
    try {
      const response = await fetch(`${apiBase}/api/messages`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ actorId, content }),
      });
      if (!response.ok) throw new Error("message failed");
      const result = await response.json() as { userMessage: Message; assistantMessage: Message };
      setMessages((current) => upsert(upsert(current, result.userMessage), result.assistantMessage));
    } catch {
      setDraft(content);
      setNotice("런타임에 연결할 수 없습니다. 잠시 뒤 다시 시도해주세요.");
    } finally {
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
    setNotice("");
    try {
      const response = await fetch(`${apiBase}/api/tasks/demo`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ actorId, durationSeconds: 12 }),
      });
      if (!response.ok) throw new Error("task failed");
      const task = await response.json() as RuntimeTask;
      setTasks((current) => upsert(current, task));
    } catch {
      setNotice("작업을 시작하려면 Go 런타임을 먼저 실행해주세요.");
    }
  }

  const activeTask = [...tasks].reverse().find((task) => task.status === "running" || task.status === "queued");
  const latestTask = activeTask || tasks.at(-1);
  const latestMemory = memories.at(-1)?.content || "대화에서 오래 남길 가치가 있는 사실을 발견하면 여기에 기억합니다.";
  const visibleEvents = useMemo(() => [...events].reverse().slice(0, 4), [events]);

  return (
    <main className="shell">
      <aside className="identity-panel">
        <div className="brand-mark" aria-hidden="true">Q</div>
        <div className="identity-copy">
          <p className="eyebrow">PERSISTENT IDENTITY</p><h1>QuanYejun</h1>
          <p className="identity-state"><span className={`status-dot ${connection}`} /> {connection === "online" ? "깨어 있음" : connection === "connecting" ? "연결 중" : "런타임 대기"}</p>
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
            <div><dt>마지막 활동</dt><dd>{events.length ? relativeTime(events.at(-1)!.timestamp) : "대기"}</dd></div>
          </dl>
        </section>
        <p className="model-label">Reasoning engine · provider agnostic</p>
      </aside>

      <section className="conversation" id="conversation">
        <header className="conversation-header">
          <div><p className="eyebrow">하나의 이어지는 대화</p><h2>소유자님과 QuanYejun</h2></div>
          <button className="task-launch" type="button" onClick={startDemoTask}>비동기 작업 실행</button>
        </header>
        <div className="conversation-body" aria-live="polite">
          <div className="date-divider"><span>오늘</span></div>
          {messages.map((message) => (
            <article className={`message ${message.role === "user" ? "user-message" : "entity-message"}`} key={message.id}>
              {message.role === "assistant" && <div className="avatar">Q</div>}
              <div>
                <div className="message-meta"><strong>{message.role === "user" ? "소유자" : "QuanYejun"}</strong><time>{shortTime(message.createdAt)}</time></div>
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
          {sending && <article className="message entity-message latest"><div className="avatar">Q</div><div><div className="message-meta"><strong>QuanYejun</strong><time>생각 중</time></div><div className="message-bubble typing"><span /><span /><span /></div></div></article>}
          <div ref={scrollAnchor} />
        </div>
        <footer className="composer-wrap">
          {notice && <p className="runtime-notice" role="alert">{notice}</p>}
          <form className="composer" onSubmit={sendMessage}>
            <label className="sr-only" htmlFor="message">메시지</label>
            <textarea id="message" name="message" value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={handleComposerKey} placeholder="QuanYejun에게 메시지 보내기" rows={1} />
            <div className="composer-actions">
              <span className={`connection-chip ${connection}`}>{connection === "online" ? "실시간 연결" : "로컬 미리보기"}</span>
              <span>Enter 전송 · Shift + Enter 줄바꿈</span>
              <button className="send-button" type="submit" aria-label="메시지 전송" disabled={!draft.trim() || sending}>↑</button>
            </div>
          </form>
          <p className="continuity-note">대화가 닫혀도 기억과 작업은 계속됩니다.</p>
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
