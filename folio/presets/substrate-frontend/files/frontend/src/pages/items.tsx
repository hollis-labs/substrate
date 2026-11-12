import { Button, EmptyState, Input, Table } from "@hollis-labs/design-components"
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
    if (!title) return
    try {
      await request("items", "POST", { title })
      form.reset()
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not add the item.")
    }
  }
  return (
    <div className="page-content stack">
      <form onSubmit={add} className="panel stack form-width">
        <Input name="title" aria-label="Title" placeholder="New item" required />
        <Button type="submit">Add</Button>
      </form>
      <Notice error={error} />
      {items && items.length === 0 && (
        <EmptyState variant="empty" title="No items yet" description="Add one above." />
      )}
      {items && items.length > 0 && (
        <Table>
          <thead>
            <tr>
              <th>Title</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <tr key={item.id}>
                <td>{item.title}</td>
                <td>{item.created_at}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  )
}
