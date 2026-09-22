import { useNavigate } from "@solidjs/router";
import { createEffect, createSignal, on, onCleanup, Show, type Component } from "solid-js";

import UsernamePicker from "~/components/UsernamePicker";
import { ApiError } from "~/lib/api";
import { fromLocalInput, toLocalInput } from "~/lib/format";
import { rooms } from "~/lib/rooms";
import type { Visibility } from "~/lib/types";
import { auth } from "~/store/auth";

type SlugState = "idle" | "checking" | "free" | "taken" | "invalid";

const SLUG_RE = /^[A-Za-z0-9-]{3,32}$/;

// NewRoom is the "start watching" page: room details, an optional first
// video, initial options and, for private rooms, invites — one submit.
const NewRoom: Component = () => {
  const navigate = useNavigate();

  // Anonymous visitors round-trip through login and come back here.
  createEffect(() => {
    if (!auth.user.loading && auth.user() === null) navigate("/login?next=/new", { replace: true });
  });

  const [name, setName] = createSignal("");
  const [slug, setSlug] = createSignal("");
  const [slugState, setSlugState] = createSignal<SlugState>("idle");
  const [visibility, setVisibility] = createSignal<Visibility>("public");
  const [description, setDescription] = createSignal("");
  const [scheduled, setScheduled] = createSignal("");
  const [firstUrl, setFirstUrl] = createSignal("");
  const [voteMode, setVoteMode] = createSignal(false);
  const [viewersCanAdd, setViewersCanAdd] = createSignal(true);
  const [invites, setInvites] = createSignal<string[]>([]);
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);

  // Live slug availability: 404 means free, anything else means taken.
  let check: number | null = null;
  createEffect(
    on(slug, (value) => {
      if (check !== null) window.clearTimeout(check);
      const s = value.trim();
      if (s === "") return setSlugState("idle");
      if (!SLUG_RE.test(s)) return setSlugState("invalid");
      setSlugState("checking");
      check = window.setTimeout(async () => {
        try {
          await rooms.get(s.toLowerCase());
          setSlugState("taken");
        } catch (err) {
          setSlugState(err instanceof ApiError && err.status === 404 ? "free" : "taken");
        }
      }, 350);
    }),
  );
  onCleanup(() => {
    if (check !== null) window.clearTimeout(check);
  });

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    if (slugState() === "taken" || slugState() === "invalid") return;
    setError(null);
    setBusy(true);
    try {
      const room = await rooms.create({
        name: name().trim(),
        slug: slug().trim() || undefined,
        visibility: visibility(),
        description: description().trim() || undefined,
        scheduledAt: fromLocalInput(scheduled()) ?? undefined,
        firstUrl: firstUrl().trim() || undefined,
        settings: { voteMode: voteMode(), viewersCanAdd: viewersCanAdd() },
        invites: visibility() === "private" ? invites() : undefined,
      });
      navigate(`/r/${room.slug}`, { state: { warnings: room.warnings } });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Show when={auth.user()}>
      <form class="new-room" onSubmit={submit}>
        <header>
          <h1>New room</h1>
          <p class="muted">Name it, drop in a link, invite people — and you are watching.</p>
        </header>

        <section class="card form">
          <h2>Room</h2>
          <label>
            Name
            <input type="text" required maxLength={80} placeholder="Friday movies" value={name()} onInput={(e) => setName(e.currentTarget.value)} autofocus />
          </label>
          <label>
            <span>
              Link <span class="muted">(optional — generated if empty)</span>
            </span>
            <div class="slug-input">
              <span class="muted">/r/</span>
              <input type="text" placeholder="friday" value={slug()} onInput={(e) => setSlug(e.currentTarget.value)} aria-describedby="slug-hint" />
            </div>
            <span id="slug-hint" class={`hint ${slugState()}`}>
              {slugState() === "checking" && "checking…"}
              {slugState() === "free" && "available"}
              {slugState() === "taken" && "already taken"}
              {slugState() === "invalid" && "3–32 letters, digits or hyphens"}
            </span>
          </label>
          <label>
            <span>
              Description <span class="muted">(optional)</span>
            </span>
            <textarea rows={2} maxLength={300} placeholder="What this room is for, when you watch" value={description()} onInput={(e) => setDescription(e.currentTarget.value)} />
          </label>
          <label>
            <span>
              First session <span class="muted">(optional)</span>
            </span>
            <div class="schedule-input">
              <input type="datetime-local" value={scheduled()} min={toLocalInput(new Date().toISOString())} onInput={(e) => setScheduled(e.currentTarget.value)} />
              <Show when={scheduled()}>
                <button type="button" class="link small" onClick={() => setScheduled("")}>
                  Clear
                </button>
              </Show>
            </div>
          </label>
          <fieldset class="radio-row">
            <label class="radio">
              <input type="radio" name="visibility" checked={visibility() === "public"} onChange={() => setVisibility("public")} />
              Public — anyone with the link can watch
            </label>
            <label class="radio">
              <input type="radio" name="visibility" checked={visibility() === "private"} onChange={() => setVisibility("private")} />
              Private — invite only
            </label>
          </fieldset>
          <Show when={visibility() === "private"}>
            <label>
              <span>
                Invite <span class="muted">(start typing a username)</span>
              </span>
              <UsernamePicker value={invites()} onChange={setInvites} exclude={[auth.user()?.username ?? ""]} />
            </label>
          </Show>
        </section>

        <section class="card form">
          <h2>Start with</h2>
          <label>
            <span>
              First video <span class="muted">(optional)</span>
            </span>
            <input type="url" placeholder="Paste a YouTube or video link" value={firstUrl()} onInput={(e) => setFirstUrl(e.currentTarget.value)} />
          </label>
          <label class="radio">
            <input type="checkbox" checked={viewersCanAdd()} onChange={(e) => setViewersCanAdd(e.currentTarget.checked)} />
            Viewers can add videos to the queue
          </label>
          <label class="radio">
            <input type="checkbox" checked={voteMode()} onChange={(e) => setVoteMode(e.currentTarget.checked)} />
            Vote mode — skip votes, queue ordered by upvotes
          </label>
        </section>

        <Show when={error()}>{(msg) => <p class="card error">{msg()}</p>}</Show>

        <div class="actions">
          <button type="submit" disabled={busy() || slugState() === "taken" || slugState() === "invalid"}>
            Create &amp; open
          </button>
          <a class="button ghost" href="/">
            Cancel
          </a>
        </div>
      </form>
    </Show>
  );
};

export default NewRoom;
