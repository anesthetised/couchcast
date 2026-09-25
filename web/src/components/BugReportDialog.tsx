import { createSignal, For, onCleanup, onMount, Show, type Component } from "solid-js";

import { BUG_CATEGORIES, bugs, type BugCategory, type BugPrefill } from "~/lib/bugs";
import { captureFrame, collect, logEvent } from "~/lib/diagnostics";
import { trapFocus } from "~/lib/focusTrap";

type Props = {
  prefill: BugPrefill;
  roomSlug: string;
  mediaId: string | null;
  signedIn: boolean;
  onClose: () => void;
};

// Categories where a picture of the screen usually says more than words.
const FRAME_BY_DEFAULT: BugCategory[] = ["playback", "subtitles"];

// BugReportDialog snapshots the diagnostics and the frame when it opens —
// the moment of the problem — and sends them with the viewer's words.
const BugReportDialog: Component<Props> = (props) => {
  let dialog!: HTMLFormElement;
  const [category, setCategory] = createSignal<BugCategory>(props.prefill.category ?? "playback");
  const [description, setDescription] = createSignal(props.prefill.description ?? "");
  const [withDetails, setWithDetails] = createSignal(true);
  const [withFrame, setWithFrame] = createSignal(FRAME_BY_DEFAULT.includes(props.prefill.category ?? "playback"));
  const [frame, setFrame] = createSignal<Blob | null>(null);
  const [frameUrl, setFrameUrl] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [done, setDone] = createSignal(false);

  const snapshot = collect();
  const preview = JSON.stringify(snapshot, null, 2);

  onMount(() => {
    trapFocus(dialog, props.onClose);
    void captureFrame().then((blob) => {
      setFrame(blob);
      if (blob) setFrameUrl(URL.createObjectURL(blob));
    });
  });
  onCleanup(() => {
    const u = frameUrl();
    if (u) URL.revokeObjectURL(u);
  });

  const pickCategory = (c: BugCategory) => {
    setCategory(c);
    setWithFrame(FRAME_BY_DEFAULT.includes(c));
  };

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const blob = withFrame() ? frame() : null;
      await bugs.create({
        category: category(),
        description: description().trim(),
        roomSlug: props.roomSlug,
        mediaId: props.mediaId ?? undefined,
        client: withDetails() ? snapshot : undefined,
        frame: blob ? await toBase64(blob) : undefined,
      });
      logEvent("app", "bug report sent");
      setDone(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="modal-backdrop" onClick={props.onClose}>
      <form class="card form modal bug-dialog" role="dialog" aria-modal="true" aria-labelledby="bug-title" ref={dialog} onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <h2 id="bug-title">Report a problem</h2>
        <Show
          when={props.signedIn}
          fallback={
            <p class="muted">
              <a href={`/login?next=${encodeURIComponent(location.pathname)}`}>Log in</a> to report a problem.
            </p>
          }
        >
          <Show when={!done()} fallback={<p class="ok">Thanks — the report is with the administrators.</p>}>
            <div class="chips" role="radiogroup" aria-label="What is it about">
              <For each={BUG_CATEGORIES}>
                {(c) => (
                  <button type="button" class="chip" role="radio" aria-checked={category() === c.id} onClick={() => pickCategory(c.id)}>
                    {c.label}
                  </button>
                )}
              </For>
            </div>
            <label>
              What happened?
              <textarea rows={4} maxLength={2000} placeholder="What you saw, and what you expected instead" value={description()} onInput={(e) => setDescription(e.currentTarget.value)} />
            </label>
            <label class="radio">
              <input type="checkbox" checked={withFrame()} disabled={!frame()} onChange={(e) => setWithFrame(e.currentTarget.checked)} />
              Attach the current frame
              <Show when={!frame()}>
                <span class="muted small"> (nothing on screen)</span>
              </Show>
            </label>
            <Show when={withFrame() && frameUrl()}>{(u) => <img class="bug-frame" src={u()} alt="Frame to attach" />}</Show>
            <label class="radio">
              <input type="checkbox" checked={withDetails()} onChange={(e) => setWithDetails(e.currentTarget.checked)} />
              Include technical details
            </label>
            <Show when={withDetails()}>
              <details class="bug-preview">
                <summary class="muted small">What will be sent</summary>
                <pre>{preview}</pre>
              </details>
            </Show>
            <Show when={error()}>{(m) => <p class="error">{m()}</p>}</Show>
          </Show>
        </Show>
        <div class="actions">
          <Show when={props.signedIn && !done()}>
            <button type="submit" disabled={busy() || (!description().trim() && !withDetails())}>
              Send
            </button>
          </Show>
          <button type="button" class="link" onClick={props.onClose}>
            Close
          </button>
        </div>
      </form>
    </div>
  );
};

function toBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""));
    r.onerror = () => reject(r.error ?? new Error("could not read the frame"));
    r.readAsDataURL(blob);
  });
}

export default BugReportDialog;
