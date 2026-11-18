/** The start module's window. A view imports only the sdk and the installed @surface components. */
import type { ModuleWeb, ViewProps } from "@hollis-labs/surface-page/sdk"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"

function Start({ desk }: ViewProps) {
  return (
    <div className="flex h-full flex-col gap-4 overflow-auto p-4">
      <Card>
        <CardHeader>
          <CardTitle>This app starts here</CardTitle>
          <CardDescription>
            A module of its own: Go in modules/start, this view in src/modules/start.tsx. The agent can call its tool,
            start_greet.
          </CardDescription>
        </CardHeader>
        <CardContent className="text-muted-foreground text-sm">
          Every control comes from the Surface registry. The Components window shows them all.
        </CardContent>
        <CardFooter className="justify-end">
          <Button variant="outline" onClick={() => void desk.open("surface.gallery")}>
            Open the Components window
          </Button>
        </CardFooter>
      </Card>
    </div>
  )
}

export default { views: { "start/main": Start } } satisfies ModuleWeb
