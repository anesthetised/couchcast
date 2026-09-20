import { createResource, createSignal, For, Show, type Component } from "solid-js";

import { inviteLinks } from "~/lib/rooms";
import { toast } from "~/lib/toast";

type Props = { slug: string };

// InviteLinks manages a room's invite links: list, mint, revoke. A new
// link is copied to the clipboard right away — the token is never shown
// again.
const InviteLinks: Component<Props> = (props) => {
  const [links, { refetch }] = createResource(() => props.slug, inviteLinks.list);
  const [expiresIn, setExpiresIn] = createSignal("7d");
  const [maxUses, setMaxUses] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const create = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const uses = maxUses().trim() === "" ? null : Number(maxUses());
      const link = await inviteLinks.create(props.slug, { expiresIn: expiresIn(), maxUses: uses });
      try {
        await navigator.clipboard.writeText(link.url ?? "");
        toast("Invite link copied.");
      } catch {
        toast(link.url ?? "Link created.");
      }
      void refetch();
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (id: string) => {
    try {
      await inviteLinks.revoke(props.slug, id);
      toast("Link revoked.");
      void refetch();
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const status = (l: { revokedAt: string | null; expiresAt: string | null; maxUses: number | null; uses: number }) => {
    if (l.revokedAt) return "revoked";
    if (l.expiresAt && new Date(l.expiresAt) <= new Date()) return "expired";
    if (l.maxUses !== null && l.uses >= l.maxUses) return "used up";
    return "";
  };

  const active = () => (links() ?? []).filter((l) => status(l) === "");

  return (
    <div class="invite-links">
      <form class="form invite-links-form" onSubmit={create}>
        <label>
          Invite link
          <div class="actions">
            <select value={expiresIn()} onChange={(e) => setExpiresIn(e.currentTarget.value)} aria-label="Expires">
              <option value="1d">expires in a day</option>
              <option value="7d">expires in a week</option>
              <option value="30d">expires in a month</option>
              <option value="">never expires</option>
            </select>
            <input type="number" min={1} max={1000} placeholder="uses (any)" value={maxUses()} onInput={(e) => setMaxUses(e.currentTarget.value)} aria-label="Maximum uses" class="uses" />
            <button type="submit" class="ghost" disabled={busy()}>
              Create & copy
            </button>
          </div>
        </label>
      </form>
      <Show when={active().length > 0}>
        <ul class="list invite-link-list">
          <For each={active()}>
            {(l) => (
              <li class="row">
                <span class="small">
                  by {l.createdBy}
                  <span class="muted">
                    {" · "}
                    {l.expiresAt ? `until ${new Date(l.expiresAt).toLocaleDateString()}` : "no expiry"}
                    {" · "}
                    {l.uses}
                    {l.maxUses !== null ? ` / ${l.maxUses}` : ""} used
                  </span>
                </span>
                <button type="button" class="link danger-text" onClick={() => void revoke(l.id)}>
                  Revoke
                </button>
              </li>
            )}
          </For>
        </ul>
      </Show>
    </div>
  );
};

export default InviteLinks;
