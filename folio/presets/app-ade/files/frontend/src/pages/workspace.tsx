/**
 * The workspace: the domain's surface on the left, the guide and the agent on the right, and the
 * attention layer over both. This page is the preset's (folio sync keeps it current); it reaches the
 * domain only through ../domain.
 */
import { AdeProvider, AgentPanel, AttentionLayer, GuideBar } from "@hollis-labs/ade-react"
import "@hollis-labs/ade-react/styles.css"
import { useEffect } from "react"
import { hub, restoreGuide, session } from "../ade/setup"
import "../ade/workspace.css"
import { labels, Surface } from "../domain"

export function WorkspacePage() {
  useEffect(() => restoreGuide(), [])
  return (
    <AdeProvider hub={hub}>
      <div className="page-content workspace">
        <section className="workspace-surface">
          <Surface />
        </section>
        <section className="workspace-agent">
          <GuideBar
            onAsk={(step) =>
              void session.send(
                `I have a question about this step of the guide: “${step.say}” Explain it another way, pointing at what it is about.`,
              )
            }
          />
          <AgentPanel session={session} title={labels.agent} placeholder={labels.placeholder} />
        </section>
      </div>
      <AttentionLayer agentLabel={labels.agent} />
    </AdeProvider>
  )
}
