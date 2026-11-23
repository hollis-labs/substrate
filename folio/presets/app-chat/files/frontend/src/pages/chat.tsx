import { Button, Input } from "@hollis-labs/design-components"
import { ChatInput, type ChatItem, ChatStream } from "@hollis-labs/kit-chat"
import { type FormEvent, useEffect, useState } from "react"
import { request } from "../api/request"

type Conversation = { id: number; title: string }
type Message = { id: number; role: "user" | "assistant"; content: string; created_at: string }
type Messages = { messages: Message[]; assistant_connected: boolean }

export function ChatPage() {
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [selected, setSelected] = useState<number | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [connected, setConnected] = useState(false)
  const [title, setTitle] = useState("")
  const [draft, setDraft] = useState("")
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  useEffect(() => {
    let cancelled = false
    void request<{ conversations: Conversation[]; assistant_connected: boolean }>("conversations")
      .then((data) => {
        if (cancelled) return
        setConversations(data.conversations)
        setConnected(data.assistant_connected)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : "Could not load conversations.")
      })
      .finally(() => {
        if (!cancelled) setBusy(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  async function select(id: number) {
    if (busy) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const data = await request<Messages>(`conversations/${id}/messages`)
      setSelected(id)
      setMessages(data.messages)
      setConnected(data.assistant_connected)
      setDraft("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load the conversation.")
    } finally {
      setBusy(false)
    }
  }

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !title.trim()) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const data = await request<{ id: number }>("conversations", "POST", { title })
      setConversations((items) => [{ id: data.id, title: title.trim() }, ...items])
      setSelected(data.id)
      setMessages([])
      setDraft("")
      setTitle("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not create a conversation.")
    } finally {
      setBusy(false)
    }
  }

  async function send(content: string) {
    if (busy || selected === null || !content.trim()) return
    setBusy(true)
    setError("")
    setNotice("")
    try {
      const result = await request<{ reply_failed: boolean; assistant_connected: boolean }>(
        `conversations/${selected}/messages`,
        "POST",
        { content },
      )
      // Clear immediately after acknowledgement, even if the following read fails:
      // retrying a refresh must not send an already saved message a second time.
      setDraft("")
      setConnected(result.assistant_connected)
      setNotice(
        result.reply_failed
          ? "Message saved, but the assistant could not reply."
          : "Message saved.",
      )
      const data = await request<Messages>(`conversations/${selected}/messages`)
      setMessages(data.messages)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not send the message.")
    } finally {
      setBusy(false)
    }
  }

  const items: ChatItem[] = messages.map((message) => ({
    kind: "message",
    id: String(message.id),
    role: message.role,
    content: message.content,
    timestamp: message.created_at,
  }))
  return (
    <section className="chat-page" aria-label="Chat" aria-busy={busy}>
      <aside className="chat-conversations" aria-label="Conversations">
        <h1>Chat</h1>
        <form onSubmit={(event) => void create(event)} className="stack">
          <label htmlFor="conversation-title">New conversation</label>
          <Input
            id="conversation-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={200}
            disabled={busy}
            required
          />
          <Button type="submit" disabled={busy || !title.trim()}>
            Create conversation
          </Button>
        </form>
        <nav aria-label="Saved conversations" className="stack">
          {conversations.map((conversation) => (
            <Button
              key={conversation.id}
              disabled={busy}
              aria-current={selected === conversation.id ? "page" : undefined}
              onClick={() => void select(conversation.id)}
            >
              {conversation.title}
            </Button>
          ))}
        </nav>
      </aside>
      <div className="chat-conversation">
        {!connected && (
          <p className="text-fg-muted">
            No assistant provider connected. Your messages are saved privately in this app.
          </p>
        )}
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}
        <ChatStream
          items={items}
          loading={busy}
          empty={
            selected === null ? "Create or choose a conversation." : "Write the first message."
          }
        />
        <ChatInput
          value={draft}
          onValueChange={setDraft}
          onSubmit={(content) => void send(content)}
          busy={busy}
          disabled={selected === null}
          placeholder="Write a message…"
        />
      </div>
    </section>
  )
}
