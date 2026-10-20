export type Message = { expiresAt?: string; id: string; role: "user" | "assistant"; content: string; createdAt: string };
export type RuntimeTask = { expiresAt?: string; id: string; title: string; status: string; progress: number; result?: string; createdAt: string; updatedAt: string };
export type Memory = { expiresAt?: string; id: string; content: string; createdAt: string };
export type RuntimeEvent = { expiresAt?: string; id: string; type: string; timestamp: string; payload: Record<string, unknown> };
export type EventCollections = { messages: Message[]; tasks: RuntimeTask[]; memories: Memory[]; events: RuntimeEvent[] };

export function upsert<T extends { id: string }>(items: T[], item: T) {
  const index = items.findIndex(current => current.id === item.id);
  if (index === -1) return [...items, item];
  return items.map((current, currentIndex) => currentIndex === index ? item : current);
}

export function applyRuntimeEvent(current: EventCollections, event: RuntimeEvent): EventCollections {
  const message = event.payload.message as Message | undefined;
  const task = event.payload.task as RuntimeTask | undefined;
  const memory = event.payload.memory as Memory | undefined;
  return {
    messages: message?.id ? upsert(current.messages, message) : current.messages,
    tasks: task?.id ? upsert(current.tasks, task) : current.tasks,
    memories: memory?.id ? upsert(current.memories, memory) : current.memories,
    events: upsert(current.events, event).slice(-20),
  };
}
