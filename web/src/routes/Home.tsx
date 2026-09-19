import { createResource, Show, type Component } from "solid-js";

import { api } from "~/lib/api";

type Health = { status: string; database?: string };

// Placeholder landing page: proves the Vite → Go proxy works end to end.
// Phase 3 replaces it with room creation, the user's rooms and invites.
const Home: Component = () => {
  const [health] = createResource(() => api<Health>("/healthz"));

  return (
    <section class="card">
      <h1>Watch together</h1>
      <p class="muted">Backend status:</p>
      <Show when={health.loading}>
        <p>checking…</p>
      </Show>
      <Show when={health.error}>
        <p class="error">{String(health.error)}</p>
      </Show>
      <Show when={health()}>{(h) => <p class="ok">{h().status}</p>}</Show>
    </section>
  );
};

export default Home;
