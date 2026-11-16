import { at } from "@hollis-labs/ade-core"
import { useAttention, useHub, useSurface } from "@hollis-labs/ade-react"
import { Button, Input } from "@hollis-labs/design-components"
import {
  type FormEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { type Board, type Card, COLUMNS, notesSurface, SURFACE } from "./notes"
import "./notes.css"

/**
 * The board. A click selects a card (shift-click adds to the selection): what is selected is the
 * person's attention, so the agent sees exactly those cards. Every change goes through the hub, as
 * the agent's do, so both see the same outcomes.
 */
export function NotesBoard({ board }: { board: Board }) {
  const hub = useHub()
  const root = useRef<HTMLDivElement>(null)
  useSyncExternalStore(board.subscribe, () => board.rev)
  const rect = useCallback(
    (id: string) =>
      root.current
        ?.querySelector<HTMLElement>(`[data-card="${CSS.escape(id)}"]`)
        ?.getBoundingClientRect() ?? null,
    [],
  )
  const surface = useSurface(() =>
    notesSurface(board, rect, () => root.current?.getBoundingClientRect() ?? null),
  )
  useEffect(() => board.subscribe(() => surface.changed()), [board, surface])
  useEffect(() => {
    const el = root.current
    if (!el) return
    const watch = new ResizeObserver(() => surface.moved())
    watch.observe(el)
    return () => watch.disconnect()
  }, [surface])

  const [picked, setPicked] = useState<string[]>([])
  const attention = useAttention()
  useEffect(() => {
    if (!attention.some((a) => a.actor === "user") && picked.length) setPicked([])
  }, [attention, picked.length])
  const pick = (ids: string[]) => {
    setPicked(ids)
    hub.attend({
      actor: "user",
      style: "select",
      anchors: ids.map((id) => at(SURFACE, "card", id, board.find(id)?.text.slice(0, 40))),
    })
  }
  const act = (action: string, input: Record<string, unknown>) =>
    void hub.act({ actor: "user", surface: SURFACE, action, input })
  const [editing, setEditing] = useState<string | null>(null)

  const add = (column: string) => (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const form = e.currentTarget
    const text = String(new FormData(form).get("text") ?? "").trim()
    if (text) act("add", { text, column })
    form.reset()
  }
  const save = (id: string) => (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const text = String(new FormData(e.currentTarget).get("text") ?? "").trim()
    if (text) act("edit", { id, text })
    setEditing(null)
  }

  return (
    <div className="notes" ref={root}>
      {COLUMNS.map((column, ci) => (
        <section key={column} className="notes-column" aria-label={column}>
          <h2>{column}</h2>
          {board.inColumn(column).map((card, i, list) => (
            <article
              key={card.id}
              data-card={card.id}
              className="notes-card"
              data-selected={picked.includes(card.id)}
              onClick={(e) =>
                pick(
                  e.shiftKey
                    ? picked.includes(card.id)
                      ? picked.filter((p) => p !== card.id)
                      : [...picked, card.id]
                    : [card.id],
                )
              }
              onKeyDown={(e) => e.key === "Enter" && pick([card.id])}
            >
              {editing === card.id ? (
                <form onSubmit={save(card.id)}>
                  <Input name="text" aria-label="Card text" defaultValue={card.text} autoFocus />
                </form>
              ) : (
                <p onDoubleClick={() => setEditing(card.id)}>{card.text}</p>
              )}
              <div className="notes-card-tools">
                {tools(card, i, list.length, ci).map(([label, action, input, off]) => (
                  <Button
                    key={label}
                    type="button"
                    className="notes-tool"
                    disabled={off}
                    onClick={() => act(action, input)}
                    aria-label={label}
                  >
                    {label}
                  </Button>
                ))}
              </div>
            </article>
          ))}
          <form onSubmit={add(column)}>
            <Input name="text" aria-label={`New card in ${column}`} placeholder="Add a card" />
          </form>
        </section>
      ))}
    </div>
  )
}

/** A card's buttons: label, action, input, and whether it is disabled. */
const tools = (card: Card, i: number, n: number, ci: number) =>
  [
    ["↑", "move", { id: card.id, column: card.column, index: i - 1 }, i === 0],
    ["↓", "move", { id: card.id, column: card.column, index: i + 1 }, i === n - 1],
    ["←", "move", { id: card.id, column: COLUMNS[ci - 1] }, ci === 0],
    ["→", "move", { id: card.id, column: COLUMNS[ci + 1] }, ci === COLUMNS.length - 1],
    ["×", "remove", { id: card.id }, false],
  ] as const satisfies readonly (readonly [string, string, Record<string, unknown>, boolean])[]
