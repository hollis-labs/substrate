import { createContext, useContext } from "react"

/** What the UI knows about the signed-in caller (from the sign-in or GET /api/identity/profile). */
export interface Session {
  /** Display name. */
  name: string
  /** Drop back to the sign-in screen. */
  signedOut: () => void
}

export const SessionContext = createContext<Session | null>(null)

export function useSession(): Session {
  const s = useContext(SessionContext)
  if (!s) throw new Error("Session context missing")
  return s
}
