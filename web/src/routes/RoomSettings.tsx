import { useNavigate, useParams } from "@solidjs/router";
import { createResource, createSignal, For, Show, type Component } from "solid-js";

import { rooms } from "~/lib/rooms";
import { isModerator, type Visibility } from "~/lib/types";

// Room administration: general settings (owner), moderators (owner),
// members, bans and invites (moderators).
const RoomSettings: Component = () => {
  const params = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [room, { mutate: setRoom }] = createResource(() => params.slug, rooms.get);
  const [members, { refetch: reloadMembers }] = createResource(() => params.slug, rooms.members);
  const [bans, { refetch: reloadBans }] = createResource(
    () => (isModerator(room()?.myRole) ? params.slug : undefined),
    rooms.bans,
  );

  const [error, setError] = createSignal<string | null>(null);
  const [notice, setNotice] = createSignal<string | null>(null);

  const run = async (label: string, fn: () => Promise<unknown>) => {
    setError(null);
    setNotice(null);
    try {
      await fn();
      setNotice(label);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const isOwner = () => room()?.myRole === "owner";

  // --- general -------------------------------------------------------------
  const [name, setName] = createSignal("");
  const [slug, setSlug] = createSignal("");
  const [visibility, setVisibility] = createSignal<Visibility>("public");
  const seedGeneral = () => {
    const r = room();
    if (!r) return;
    setName(r.name);
    setSlug(r.slug);
    setVisibility(r.visibility);
  };

  const saveGeneral = (e: SubmitEvent) => {
    e.preventDefault();
    void run("Saved.", async () => {
      const updated = await rooms.update(params.slug, {
        name: name().trim(),
        slug: slug().trim(),
        visibility: visibility(),
      });
      setRoom(updated);
      if (updated.slug !== params.slug) navigate(`/r/${updated.slug}/settings`, { replace: true });
    });
  };

  const deleteRoom = () => {
    if (!confirm("Delete this room? This cannot be undone.")) return;
    void run("Deleted.", async () => {
      await rooms.remove(params.slug);
      navigate("/", { replace: true });
    });
  };

  // --- people --------------------------------------------------------------
  const [inviteName, setInviteName] = createSignal("");
  const [banName, setBanName] = createSignal("");
  const [banReason, setBanReason] = createSignal("");

  const invite = (e: SubmitEvent) => {
    e.preventDefault();
    void run(`Invited ${inviteName()}.`, async () => {
      await rooms.invite(params.slug, inviteName().trim());
      setInviteName("");
    });
  };

  const ban = (e: SubmitEvent) => {
    e.preventDefault();
    void run(`Banned ${banName()}.`, async () => {
      await rooms.ban(params.slug, banName().trim(), banReason().trim());
      setBanName("");
      setBanReason("");
      void reloadBans();
      void reloadMembers();
    });
  };

  return (
    <Show when={room()} fallback={<p class="muted">{room.error ? String(room.error) : "Loading…"}</p>}>
      {(r) => {
        seedGeneral();
        return (
          <div class="stack">
            <header class="card room-header">
              <div>
                <h1>{r().name} — settings</h1>
                <p class="muted">
                  <a href={`/r/${r().slug}`}>← back to room</a>
                </p>
              </div>
            </header>

            <Show when={error()}>{(msg) => <p class="card error">{msg()}</p>}</Show>
            <Show when={notice()}>{(msg) => <p class="card ok">{msg()}</p>}</Show>

            <Show when={isOwner()}>
              <form class="card form" onSubmit={saveGeneral}>
                <h2>General</h2>
                <label>
                  Name
                  <input type="text" required maxLength={80} value={name()} onInput={(e) => setName(e.currentTarget.value)} />
                </label>
                <label>
                  Link
                  <div class="slug-input">
                    <span class="muted">/r/</span>
                    <input type="text" required pattern="[A-Za-z0-9-]{3,32}" value={slug()} onInput={(e) => setSlug(e.currentTarget.value)} />
                  </div>
                </label>
                <fieldset class="radio-row">
                  <label class="radio">
                    <input type="radio" name="vis" checked={visibility() === "public"} onChange={() => setVisibility("public")} />
                    Public
                  </label>
                  <label class="radio">
                    <input type="radio" name="vis" checked={visibility() === "private"} onChange={() => setVisibility("private")} />
                    Private
                  </label>
                </fieldset>
                <div class="actions">
                  <button type="submit">Save</button>
                  <button type="button" class="danger" onClick={deleteRoom}>
                    Delete room
                  </button>
                </div>
              </form>
            </Show>

            <section class="card">
              <h2>Members</h2>
              <ul class="list">
                <For each={members() ?? []}>
                  {(m) => (
                    <li class="row">
                      <span>
                        {m.username} <span class="muted">{m.role}</span>
                      </span>
                      <span class="actions">
                        <Show when={isOwner() && m.role === "member"}>
                          <button type="button" class="link" onClick={() => void run(`${m.username} is now a moderator.`, async () => { await rooms.addModerator(params.slug, m.username); void reloadMembers(); })}>
                            Make moderator
                          </button>
                        </Show>
                        <Show when={isOwner() && m.role === "moderator"}>
                          <button type="button" class="link" onClick={() => void run(`${m.username} is no longer a moderator.`, async () => { await rooms.removeModerator(params.slug, m.username); void reloadMembers(); })}>
                            Remove moderator
                          </button>
                        </Show>
                        <Show when={m.role !== "owner" && (isOwner() || m.role === "member")}>
                          <button type="button" class="link" onClick={() => void run(`Removed ${m.username}.`, async () => { await rooms.removeMember(params.slug, m.username); void reloadMembers(); })}>
                            Remove
                          </button>
                        </Show>
                      </span>
                    </li>
                  )}
                </For>
              </ul>
            </section>

            <form class="card form" onSubmit={invite}>
              <h2>Invite</h2>
              <label>
                Username
                <input type="text" required value={inviteName()} onInput={(e) => setInviteName(e.currentTarget.value)} />
              </label>
              <button type="submit">Send invite</button>
            </form>

            <section class="card">
              <h2>Bans</h2>
              <ul class="list">
                <For each={bans() ?? []} fallback={<li class="muted">Nobody is banned.</li>}>
                  {(b) => (
                    <li class="row">
                      <span>
                        {b.username} <span class="muted">{b.reason}</span>
                      </span>
                      <button type="button" class="link" onClick={() => void run(`Unbanned ${b.username}.`, async () => { await rooms.unban(params.slug, b.username); void reloadBans(); })}>
                        Unban
                      </button>
                    </li>
                  )}
                </For>
              </ul>
              <form class="form" onSubmit={ban}>
                <label>
                  Username
                  <input type="text" required value={banName()} onInput={(e) => setBanName(e.currentTarget.value)} />
                </label>
                <label>
                  Reason <span class="muted">(optional)</span>
                  <input type="text" maxLength={200} value={banReason()} onInput={(e) => setBanReason(e.currentTarget.value)} />
                </label>
                <button type="submit" class="danger">
                  Ban
                </button>
              </form>
            </section>
          </div>
        );
      }}
    </Show>
  );
};

export default RoomSettings;
