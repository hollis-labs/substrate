/**
 * The starter domain: a board of notes in columns, and what the application tells ADE about it.
 * This folder is yours (folio sync never writes it): replace the board with your own thing and
 * keep index.ts exporting the same names.
 */
import type { ActOutcome, Anchor, Level, SurfaceAdapter } from "@hollis-labs/ade-core"

export const SURFACE = "notes"
export const COLUMNS = ["ideas", "doing", "done"] as const
export interface Card {
  id: string
  text: string
  column: string
}

/** The board's state, kept in this browser. */
export class Board {
  cards: Card[] = []
  rev = 0
  private listeners = new Set<() => void>()
  constructor(private key: string) {
    try {
      this.cards = JSON.parse(window.localStorage.getItem(key) ?? "[]") as Card[]
    } catch {
      this.cards = []
    }
  }
  subscribe = (fn: () => void) => {
    this.listeners.add(fn)
    return () => {
      this.listeners.delete(fn)
    }
  }
  private changed() {
    this.rev++
    try {
      window.localStorage.setItem(this.key, JSON.stringify(this.cards))
    } catch {
      // Not kept this time.
    }
    for (const fn of this.listeners) fn()
  }
  find = (id: unknown) => this.cards.find((c) => c.id === id)
  inColumn = (column: string) => this.cards.filter((c) => c.column === column)
  add(text: string, column: string = COLUMNS[0]): Card {
    const card = { id: `n${Date.now().toString(36)}${this.cards.length}`, text, column }
    this.cards.push(card)
    this.changed()
    return card
  }
  edit(card: Card, text: string) {
    card.text = text
    this.changed()
  }
  /** Moves a card to a column, at a position in it (0 is the top; left out: the end). */
  move(card: Card, column: string, index?: number) {
    this.cards = this.cards.filter((c) => c !== card)
    card.column = column
    const peers = this.inColumn(column)
    const before = index === undefined ? undefined : peers[index]
    const at = before ? this.cards.indexOf(before) : this.cards.length
    this.cards.splice(at, 0, card)
    this.changed()
  }
  remove(card: Card) {
    this.cards = this.cards.filter((c) => c !== card)
    this.changed()
  }
}

const obj = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const column = { type: "string", enum: [...COLUMNS] }
const id = str("The card.")
type Props = Record<string, unknown>
const action = (name: string, level: Level, description: string, props: Props, req: string[]) => ({
  name,
  level,
  description,
  input_schema: obj(props, req),
})
const check = (name: string, description: string, props: Props, req: string[] = []) => ({
  name,
  description,
  args_schema: obj(props, req),
})
const no = (reason: string): ActOutcome => ({ outcome: "rejected", code: "invalid", reason })

/** The board as a surface. `rect` says where a card is on screen now (the view knows). */
export function notesSurface(
  board: Board,
  rect: (id: string) => DOMRect | null,
  whole: () => DOMRect | null,
): SurfaceAdapter {
  const box = (r: DOMRect | null) => (r ? { x: r.left, y: r.top, w: r.width, h: r.height } : null)
  return {
    info: { id: SURFACE, app: "notes", title: "Notes board" },
    observe: () => ({
      resource: { ref: "notes://board", rev: String(board.rev) },
      summary: `${board.cards.length} cards. ${COLUMNS.map((c) => `${c}: ${board.inColumn(c).length}`).join(", ")}. The parts are the cards: kind "card", id as listed.`,
      state: { columns: COLUMNS.map((c) => ({ column: c, cards: board.inColumn(c) })) },
      targets: board.cards.map((c) => ({ kind: "card", id: c.id, label: c.text.slice(0, 60) })),
      actions: [
        action("add", "act", "Add a card.", { text: str("What it says."), column }, ["text"]),
        action("edit", "act", "Change what a card says.", { id, text: str("The new text.") }, [
          "id",
          "text",
        ]),
        action(
          "move",
          "act",
          "Move a card to a column, at a position in it (0 is the top; left out: the end).",
          { id, column, index: { type: "number", minimum: 0 } },
          ["id", "column"],
        ),
        action("remove", "write", "Remove a card for good.", { id }, ["id"]),
      ],
      checks: [
        check("card_exists", "A card with this text (or id) is on the board.", {
          text: str("Words on the card."),
          id,
        }),
        check(
          "card_in_column",
          "A card is in a column.",
          { id, text: str("Words on the card."), column },
          ["column"],
        ),
        check(
          "count_at_least",
          "A column (or the board) has at least this many cards.",
          { count: { type: "number" }, column },
          ["count"],
        ),
      ],
    }),
    act: (action, input) => {
      const text = typeof input.text === "string" ? input.text.trim() : ""
      const col = typeof input.column === "string" ? input.column : undefined
      if (col && !(COLUMNS as readonly string[]).includes(col))
        return no(`There is no column "${col}".`)
      if (action === "add")
        return text
          ? { outcome: "applied", data: { id: board.add(text, col).id } }
          : no("A card needs text.")
      const card = board.find(input.id)
      if (!card)
        return no(`There is no card "${String(input.id)}". Look at the board for the cards it has.`)
      if (action === "edit") {
        if (!text) return no("A card needs text.")
        board.edit(card, text)
      } else if (action === "move") {
        if (!col) return no("Say which column.")
        board.move(card, col, typeof input.index === "number" ? input.index : undefined)
      } else if (action === "remove") board.remove(card)
      else return no(`The board does not do "${action}".`)
      return { outcome: "applied" }
    },
    check: (name, args) => {
      const match = (c: Card) =>
        (args.id === undefined || c.id === args.id) &&
        (typeof args.text !== "string" || c.text.toLowerCase().includes(args.text.toLowerCase()))
      if (name === "card_exists") return board.cards.some(match)
      if (name === "card_in_column")
        return board.cards.some((c) => match(c) && c.column === args.column)
      if (name === "count_at_least") {
        const list = typeof args.column === "string" ? board.inColumn(args.column) : board.cards
        return list.length >= Number(args.count ?? 0)
      }
      return false
    },
    has: (t) => t.kind === "card" && !!board.find(t.id),
    bounds: () => box(whole()),
    resolve: (anchor: Anchor) =>
      anchor.target?.kind === "card" ? box(rect(anchor.target.id)) : null,
    describe: (anchor) => {
      const c = board.find(anchor.target?.id)
      return c ? `the card "${c.text.slice(0, 40)}" in ${c.column}` : ""
    },
  }
}
