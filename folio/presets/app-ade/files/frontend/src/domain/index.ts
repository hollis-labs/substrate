/**
 * Your domain, as the workspace sees it. The workspace page (pages/workspace.tsx, the preset's)
 * imports only these names from here, so you can replace everything in this folder as long as
 * index.ts still exports them.
 */
import type { Hub } from "@hollis-labs/ade-core"
import { createElement } from "react"
import { NotesBoard } from "./board"
import { Board, SURFACE } from "./notes"

const board = new Board("notes-board")

/** The surface's id: what the context and the guide are about. */
export { SURFACE }

/** The words the panel uses. */
export const labels = {
  agent: "Assistant",
  placeholder: "Ask about the board, or ask for a change…",
}

/** The domain's view: the left side of the workspace. */
export function Surface() {
  return createElement(NotesBoard, { board })
}

/** What the model is shown with each message: the board as it is, and what is selected. */
export function context(hub: Hub): unknown {
  if (!hub.has(SURFACE)) return undefined
  const { actions: _a, checks: _c, id: _i, ...seen } = hub.observe(SURFACE)
  return { board: seen }
}
