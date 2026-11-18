import { AppFrame } from "@/components/ui/app-frame"
import { Button } from "@/components/ui/button"
import { Home } from "./views/home"

/** The frame: a bar across the top and the view under it. The frame draws the bar; this is what is in it. */
export function App() {
  return (
    <AppFrame
      bar={
        <>
          <span className="font-medium text-sm">{document.title}</span>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            render={<a href="#/gallery">Components</a>}
            nativeButton={false}
          />
        </>
      }
    >
      <Home />
    </AppFrame>
  )
}
