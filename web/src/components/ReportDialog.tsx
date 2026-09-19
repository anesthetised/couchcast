import { createSignal, Show, type Component } from "solid-js";

import { reports, type ReportReason } from "~/lib/admin";

type Props = { mediaId: string; title: string; roomSlug: string; onClose: () => void };

// ReportDialog files a complaint about a media item.
const ReportDialog: Component<Props> = (props) => {
  const [reason, setReason] = createSignal<ReportReason>("other");
  const [comment, setComment] = createSignal("");
  const [error, setError] = createSignal<string | null>(null);
  const [done, setDone] = createSignal(false);

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await reports.create(props.mediaId, reason(), comment().trim(), props.roomSlug);
      setDone(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  return (
    <div class="modal-backdrop" onClick={props.onClose}>
      <form class="card form modal" onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <h2>Report video</h2>
        <p class="muted small">{props.title}</p>
        <Show when={!done()} fallback={<p class="ok">Thanks, an administrator will review it.</p>}>
          <label>
            Reason
            <select value={reason()} onChange={(e) => setReason(e.currentTarget.value as ReportReason)}>
              <option value="copyright">Copyright</option>
              <option value="illegal">Illegal content</option>
              <option value="nsfw">NSFW</option>
              <option value="other">Other</option>
            </select>
          </label>
          <label>
            <span>
              Comment <span class="muted">(optional)</span>
            </span>
            <input type="text" maxLength={500} value={comment()} onInput={(e) => setComment(e.currentTarget.value)} />
          </label>
          <Show when={error()}>{(m) => <p class="error">{m()}</p>}</Show>
        </Show>
        <div class="actions">
          <Show when={!done()}>
            <button type="submit">Send report</button>
          </Show>
          <button type="button" class="link" onClick={props.onClose}>
            Close
          </button>
        </div>
      </form>
    </div>
  );
};

export default ReportDialog;
