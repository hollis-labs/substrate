/** The starter view: replace it with the app's own. Built only from installed @surface components. */
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"

export function Home() {
  const [name, setName] = useState("")
  const [saved, setSaved] = useState("")
  return (
    <div className="mx-auto flex w-full max-w-md flex-col gap-4 p-6">
      <Card>
        <CardHeader>
          <CardTitle>Hello{saved ? `, ${saved}` : ""}</CardTitle>
          <CardDescription>This view is made of registry components. Make it yours.</CardDescription>
        </CardHeader>
        <CardContent>
          <Input
            aria-label="Your name"
            placeholder="Your name"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </CardContent>
        <CardFooter className="justify-end gap-2">
          <Dialog>
            <DialogTrigger render={<Button variant="outline">About</Button>} />
            <DialogContent>
              <DialogHeader>
                <DialogTitle>About this app</DialogTitle>
                <DialogDescription>
                  Its components come from the Surface registry; ui.lock.json says which release.
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <DialogClose render={<Button>Close</Button>} />
              </DialogFooter>
            </DialogContent>
          </Dialog>
          <Button disabled={!name.trim()} onClick={() => setSaved(name.trim())}>
            Save
          </Button>
        </CardFooter>
      </Card>
    </div>
  )
}
