import { useNavigate } from "@solidjs/router";
import { createResource, For, Show, type Component } from "solid-js";

import { invites } from "~/lib/rooms";

const InvitesInbox: Component = () => {
  const navigate = useNavigate();
  const [list, { refetch }] = createResource(invites.mine);

  const accept = async (id: string) => {
    const { roomSlug } = await invites.accept(id);
    navigate(`/r/${roomSlug}`);
  };

  const decline = async (id: string) => {
    await invites.decline(id);
    void refetch();
  };

  return (
    <Show when={(list() ?? []).length > 0}>
      <section class="card">
        <h2>Invites</h2>
        <ul class="list">
          <For each={list()}>
            {(inv) => (
              <li class="row">
                <span>
                  <strong>{inv.roomName}</strong> <span class="muted">from {inv.inviter}</span>
                </span>
                <span class="actions">
                  <button type="button" onClick={() => void accept(inv.id)}>
                    Accept
                  </button>
                  <button type="button" class="link" onClick={() => void decline(inv.id)}>
                    Decline
                  </button>
                </span>
              </li>
            )}
          </For>
        </ul>
      </section>
    </Show>
  );
};

export default InvitesInbox;
