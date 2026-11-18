import "./index.css"
import { StrictMode, useEffect, useState } from "react"
import { createRoot } from "react-dom/client"
import { App } from "./App"
import { Gallery } from "./gallery/gallery"

// Dark is the class "dark" on <html>; follow the system until the app has a setting for it.
const dark = window.matchMedia("(prefers-color-scheme: dark)")
const applyTheme = () => document.documentElement.classList.toggle("dark", dark.matches)
applyTheme()
dark.addEventListener("change", applyTheme)

/** #/gallery is the gallery page; everything else is the app. */
function Root() {
  const [hash, setHash] = useState(window.location.hash)
  useEffect(() => {
    const onHash = () => setHash(window.location.hash)
    window.addEventListener("hashchange", onHash)
    return () => window.removeEventListener("hashchange", onHash)
  }, [])
  return hash === "#/gallery" ? <Gallery /> : <App />
}

const root = document.getElementById("root")
if (!root) throw new Error("no #root")
createRoot(root).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
