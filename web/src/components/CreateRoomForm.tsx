import { useNavigate } from "@solidjs/router";
import { createSignal, Show, type Component } from "solid-js";

import { rooms } from "~/lib/rooms";
import type { Visibility } from "~/lib/types";

type Props = { onCancel?: () => void };

const CreateRoomForm: Component<Props> = (props) => {
  const navigate = useNavigate();
  const [name, setName] = createSignal("");
  const [slug, setSlug] = createSignal("");
  const [visibility, setVisibility] = createSignal<Visibility>("public");
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const room = await rooms.create({
        name: name().trim(),
        slug: slug().trim() || undefined,
        visibility: visibility(),
      });
      navigate(`/r/${room.slug}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form class="card form" onSubmit={submit}>
      <div class="create-grid">
        <label>
          Name
          <input type="text" required maxLength={80} placeholder="Friday movies" value={name()} onInput={(e) => setName(e.currentTarget.value)} />
        </label>
        <label>
          <span>
            Custom link <span class="muted">(optional)</span>
          </span>
          <div class="slug-input">
            <span class="muted">/r/</span>
            <input type="text" placeholder="generated if empty" pattern="[A-Za-z0-9-]{3,32}" value={slug()} onInput={(e) => setSlug(e.currentTarget.value)} />
          </div>
        </label>
      </div>
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
      <Show when={error()}>{(msg) => <p class="error">{msg()}</p>}</Show>
      <div class="actions">
        <button type="submit" disabled={busy()}>
          Create room
        </button>
        <Show when={props.onCancel}>
          <button type="button" class="ghost" onClick={props.onCancel}>
            Cancel
          </button>
        </Show>
      </div>
    </form>
  );
};

export default CreateRoomForm;
