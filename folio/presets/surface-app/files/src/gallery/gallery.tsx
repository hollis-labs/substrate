/**
 * The gallery: every installed component in its variants and states, from fixtures, so the app's look can
 * be checked by eye and its screenshots compared later. Add a specimen when you install a component.
 */
import type { ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
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

const VARIANTS = ["default", "outline", "secondary", "ghost", "destructive", "link"] as const
const SIZES = ["xs", "sm", "default", "lg"] as const

function Specimen({ name, children }: { name: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3" data-gallery-section={name}>
      <h2 className="font-medium text-muted-foreground text-sm">{name}</h2>
      <div className="flex flex-wrap items-center gap-3">{children}</div>
    </section>
  )
}

export function Gallery() {
  return (
    <div className="h-full overflow-auto bg-background text-foreground">
      <div className="mx-auto flex max-w-4xl flex-col gap-8 p-6">
        <header className="flex items-center gap-2">
          <h1 className="font-semibold text-lg">Components</h1>
          <Button variant="link" className="ml-auto" render={<a href="#/">Back to the app</a>} nativeButton={false} />
        </header>

        <Specimen name="button: variants">
          {VARIANTS.map((v) => (
            <Button key={v} variant={v}>
              {v}
            </Button>
          ))}
        </Specimen>
        <Specimen name="button: sizes">
          {SIZES.map((s) => (
            <Button key={s} size={s}>
              {s}
            </Button>
          ))}
        </Specimen>
        <Specimen name="button: disabled">
          {VARIANTS.map((v) => (
            <Button key={v} variant={v} disabled>
              {v}
            </Button>
          ))}
        </Specimen>

        <Specimen name="input">
          <Input aria-label="empty" placeholder="Placeholder" className="w-56" />
          <Input aria-label="filled" defaultValue="Filled" className="w-56" />
          <Input aria-label="invalid" defaultValue="Invalid" aria-invalid className="w-56" />
          <Input aria-label="disabled" defaultValue="Disabled" disabled className="w-56" />
        </Specimen>

        <Specimen name="card">
          <Card className="w-80">
            <CardHeader>
              <CardTitle>Card title</CardTitle>
              <CardDescription>A description under the title.</CardDescription>
              <CardAction>
                <Button size="sm" variant="outline">
                  Action
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent className="text-sm">The card's content.</CardContent>
            <CardFooter className="justify-end">
              <Button size="sm">Footer button</Button>
            </CardFooter>
          </Card>
        </Specimen>

        <Specimen name="dialog">
          <Dialog>
            <DialogTrigger render={<Button variant="outline">Open the dialog</Button>} />
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Dialog title</DialogTitle>
                <DialogDescription>What the dialog asks or says.</DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <DialogClose render={<Button variant="outline">Cancel</Button>} />
                <DialogClose render={<Button>Confirm</Button>} />
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </Specimen>
      </div>
    </div>
  )
}
