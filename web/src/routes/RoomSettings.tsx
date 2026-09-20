import { useNavigate, useParams } from "@solidjs/router";
import { createResource, createSignal, For, Show, type Component } from "solid-js";

import UsernamePicker from "~/components/UsernamePicker";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import { isModerator, type Visibility } from "~/lib/types";
import { auth } from "~/store/auth";

// Room administration: general settings (owner), people (members, bans,
// invites — moderators). Sections, not cards; one table for people.
const RoomSettings: Component = () => {
  const params = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [room, { mutate: setRoom }] = createResource(() => params.slug, rooms.get);
  const [members, { refetch: reloadMembers }] = createResource(() => params.slug, rooms.members);
  const [bans, { refetch: reloadBans }] = createResource(
    () => (isModerator(room()?.myRole) ? params.slug : undefined),
    rooms.bans,
  );

  const run = async (label: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
      toast(label);
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const isOwner = () => room()?.myRole === "owner";

  // --- general -------------------------------------------------------------
  const [name, setName] = createSignal("");
  const [slug, setSlug] = createSignal("");
  const [visibility, setVisibility] = createSignal<Visibility>("public");
  const [description, setDescription] = createSignal("");
  const seedGeneral = () => {
    const r = room();
    if (!r) return;
    setName(r.name);
    setSlug(r.slug);
    setVisibility(r.visibility);
    setDescription(r.description);
  };

  const saveGeneral = (e: SubmitEvent) => {
    e.preventDefault();
    void run("Saved.", async () => {
      const updated = await rooms.update(params.slug, { name: name().trim(), slug: slug().trim(), visibility: visibility(), description: description().trim() });
      setRoom(updated);
      if (updated.slug !== params.slug) navigate(`/r/${updated.slug}/settings`, { replace: true });
    });
  };

  const transfer = (username: string) => {
    if (!confirm(`Make ${username} the owner? You will stay as a moderator.`)) return;
    void run(`${username} now owns this room.`, async () => {
      await rooms.transfer(params.slug, username);
      navigate(`/r/${params.slug}`, { replace: true });
    });
  };

  const leave = () => {
    if (!confirm("Leave this room?")) return;
    void run("You left the room.", async () => {
      await rooms.leave(params.slug, auth.user()!.username);
      navigate("/", { replace: true });
    });
  };

  const deleteRoom = () => {
    if (!confirm("Delete this room? This cannot be undone.")) return;
    void run("Room deleted.", async () => {
      await rooms.remove(params.slug);
      navigate("/", { replace: true });
    });
  };

  // --- people --------------------------------------------------------------
  const [inviteNames, setInviteNames] = createSignal<string[]>([]);
  const [banNames, setBanNames] = createSignal<string[]>([]);
  const [banReason, setBanReason] = createSignal("");

  const invite = (e: SubmitEvent) => {
    e.preventDefault();
    const names = inviteNames();
    if (!names.length) return;
    void run(`Invited ${names.join(", ")}.`, async () => {
      for (const n of names) await rooms.invite(params.slug, n);
      setInviteNames([]);
    });
  };

  const ban = (e: SubmitEvent) => {
    e.preventDefault();
    const names = banNames();
    if (!names.length) return;
    void run(`Banned ${names.join(", ")}.`, async () => {
      for (const n of names) await rooms.ban(params.slug, n, banReason().trim());
      setBanNames([]);
      setBanReason("");
      void reloadBans();
      void reloadMembers();
    });
  };

  const banned = (username: string) => (bans() ?? []).find((b) => b.username === username);

  return (
    <Show when={room()} fallback={<p class="muted">{room.error ? String(room.error) : "Loading…"}</p>}>
      {(r) => {
        seedGeneral();
        return (
          <div class="settings">
            <header class="room-head">
              <div class="room-title">
                <h1>{r().name}</h1>
                <div class="room-meta">
                  <a class="muted small" href={`/r/${r().slug}`}>
                    ← back to room
                  </a>
                </div>
              </div>
            </header>

            <Show when={isOwner()}>
              <form class="settings-section" onSubmit={saveGeneral}>
                <div class="settings-label">
                  <h2>General</h2>
                  <p class="muted small">Name, link and who can enter.</p>
                </div>
                <div class="settings-body form">
                  <label>
                    Name
                    <input type="text" required maxLength={80} value={name()} onInput={(e) => setName(e.currentTarget.value)} />
                  </label>
                  <label>
                    <span>
                      Description <span class="muted">(optional)</span>
                    </span>
                    <textarea rows={2} maxLength={300} value={description()} onInput={(e) => setDescription(e.currentTarget.value)} />
                  </label>
                  <label>
                    Link
                    <div class="slug-input">
                      <span class="muted">/r/</span>
                      <input type="text" required pattern="[A-Za-z0-9\-]{3,32}" value={slug()} onInput={(e) => setSlug(e.currentTarget.value)} />
                    </div>
                  </label>
                  <fieldset class="radio-row">
                    <label class="radio">
                      <input type="radio" name="vis" checked={visibility() === "public"} onChange={() => setVisibility("public")} />
                      Public — anyone with the link can watch
                    </label>
                    <label class="radio">
                      <input type="radio" name="vis" checked={visibility() === "private"} onChange={() => setVisibility("private")} />
                      Private — invite only
                    </label>
                  </fieldset>
                  <div class="actions">
                    <button type="submit">Save</button>
                  </div>
                </div>
              </form>
            </Show>

            <section class="settings-section">
              <div class="settings-label">
                <h2>People</h2>
                <p class="muted small">Members, roles and bans.</p>
              </div>
              <div class="settings-body">
                <table class="table">
                  <thead>
                    <tr>
                      <th>User</th>
                      <th>Role</th>
                      <th>Since</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    <For each={members() ?? []}>
                      {(m) => (
                        <tr class={banned(m.username) ? "muted" : ""}>
                          <td>
                            {m.username}
                            <Show when={banned(m.username)}>
                              {(b) => <span class="badge status-failed"> banned{b().reason ? ` · ${b().reason}` : ""}</span>}
                            </Show>
                          </td>
                          <td>
                            <span class={`badge ${m.role !== "member" ? "role" : ""}`}>{m.role}</span>
                          </td>
                          <td class="muted small">{new Date(m.joinedAt).toLocaleDateString()}</td>
                          <td class="row-actions">
                            <Show when={isOwner() && m.role === "member"}>
                              <button type="button" class="link" onClick={() => void run(`${m.username} is now a moderator.`, async () => { await rooms.addModerator(params.slug, m.username); void reloadMembers(); })}>
                                Make moderator
                              </button>
                            </Show>
                            <Show when={isOwner() && m.role !== "owner"}>
                              <button type="button" class="link" onClick={() => transfer(m.username)}>
                                Make owner
                              </button>
                            </Show>
                            <Show when={isOwner() && m.role === "moderator"}>
                              <button type="button" class="link" onClick={() => void run(`${m.username} is no longer a moderator.`, async () => { await rooms.removeModerator(params.slug, m.username); void reloadMembers(); })}>
                                Demote
                              </button>
                            </Show>
                            <Show when={m.role !== "owner" && (isOwner() || m.role === "member")}>
                              <Show
                                when={banned(m.username)}
                                fallback={
                                  <button type="button" class="link danger-text" onClick={() => void run(`Banned ${m.username}.`, async () => { await rooms.ban(params.slug, m.username, ""); void reloadBans(); })}>
                                    Ban
                                  </button>
                                }
                              >
                                <button type="button" class="link" onClick={() => void run(`Unbanned ${m.username}.`, async () => { await rooms.unban(params.slug, m.username); void reloadBans(); })}>
                                  Unban
                                </button>
                              </Show>
                              <button type="button" class="link danger-text" onClick={() => void run(`Removed ${m.username}.`, async () => { await rooms.removeMember(params.slug, m.username); void reloadMembers(); })}>
                                Remove
                              </button>
                            </Show>
                          </td>
                        </tr>
                      )}
                    </For>
                    <For each={(bans() ?? []).filter((b) => !(members() ?? []).some((m) => m.username === b.username))}>
                      {(b) => (
                        <tr class="muted">
                          <td>
                            {b.username} <span class="badge status-failed">banned{b.reason ? ` · ${b.reason}` : ""}</span>
                          </td>
                          <td>
                            <span class="badge">not a member</span>
                          </td>
                          <td class="small">{new Date(b.createdAt).toLocaleDateString()}</td>
                          <td class="row-actions">
                            <button type="button" class="link" onClick={() => void run(`Unbanned ${b.username}.`, async () => { await rooms.unban(params.slug, b.username); void reloadBans(); })}>
                              Unban
                            </button>
                          </td>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>

                <div class="settings-forms">
                  <form class="form" onSubmit={invite}>
                    <label>
                      Invite
                      <UsernamePicker value={inviteNames()} onChange={setInviteNames} placeholder="username" />
                    </label>
                    <div class="actions">
                      <button type="submit" class="ghost" disabled={inviteNames().length === 0}>
                        Send invite{inviteNames().length === 1 ? "" : "s"}
                      </button>
                    </div>
                  </form>
                  <form class="form" onSubmit={ban}>
                    <label>
                      Ban
                      <UsernamePicker value={banNames()} onChange={setBanNames} placeholder="username" />
                    </label>
                    <label>
                      <span>
                        Reason <span class="muted">(optional)</span>
                      </span>
                      <input type="text" maxLength={200} value={banReason()} onInput={(e) => setBanReason(e.currentTarget.value)} />
                    </label>
                    <div class="actions">
                      <button type="submit" class="danger" disabled={banNames().length === 0}>
                        Ban
                      </button>
                    </div>
                  </form>
                </div>
              </div>
            </section>

            <Show when={!isOwner()}>
              <section class="settings-section">
                <div class="settings-label">
                  <h2>Membership</h2>
                  <p class="muted small">Leaving removes you from the room; a moderator can invite you back.</p>
                </div>
                <div class="settings-body">
                  <button type="button" class="ghost" onClick={leave}>
                    Leave room
                  </button>
                </div>
              </section>
            </Show>

            <Show when={isOwner()}>
              <section class="settings-section">
                <div class="settings-label">
                  <h2>Danger zone</h2>
                  <p class="muted small">Deleting removes the queue, chat and memberships.</p>
                </div>
                <div class="settings-body">
                  <button type="button" class="danger" onClick={deleteRoom}>
                    Delete room
                  </button>
                </div>
              </section>
            </Show>
          </div>
        );
      }}
    </Show>
  );
};

export default RoomSettings;
