import {
  Button,
  EmptyState,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@hollis-labs/design-components"
import { type FormEvent, useCallback, useEffect, useState } from "react"
import { request } from "../api/request"
import { Notice } from "../identity/forms"

interface Item {
  id: number
  title: string
  created_at: string
}

/** The one surface of the template: list and create items (GET and POST /api/items). */
export function ItemsPage() {
  const [items, setItems] = useState<Item[] | null>(null)
  const [error, setError] = useState("")
  const [adding, setAdding] = useState(false)
  const load = useCallback(async () => {
    try {
      const data = await request<{ items: Item[] }>("items")
      setItems(data.items)
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load items.")
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])
  async function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const title = String(new FormData(form).get("title") ?? "").trim()
    if (!title || adding) return
    setAdding(true)
    try {
      await request("items", "POST", { title })
      form.reset()
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not add the item.")
    } finally {
      setAdding(false)
    }
  }
  return (
    <div className="page-content stack">
      <form onSubmit={add} className="panel stack form-width">
        <Input name="title" aria-label="Title" placeholder="New item" required />
        <Button type="submit" disabled={adding}>
          {adding ? "Adding…" : "Add"}
        </Button>
      </form>
      <Notice error={error} />
      {items && items.length === 0 && (
        <EmptyState variant="empty" title="No items yet" description="Add one above." />
      )}
      {items && items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Title</TableHead>
              <TableHead>Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>{item.title}</TableCell>
                <TableCell>{new Date(item.created_at).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
