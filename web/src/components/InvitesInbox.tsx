import { useNavigate } from "@solidjs/router";
import { createResource, For, Show, type Component } from "solid-js";

import { invites } from "~/lib/rooms";

// Compact banner with pending invites; hidden when there are none.
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
      <section class="banner">
        <strong>
          {list()!.length} pending invite{list()!.length === 1 ? "" : "s"}
        </strong>
        <ul class="list">
          <For each={list()}>
            {(inv) => (
              <li class="row">
                <span>
                  {inv.roomName} <span class="muted small">from {inv.inviter}</span>
                </span>
                <button type="button" onClick={() => void accept(inv.id)}>
                  Accept
                </button>
                <button type="button" class="link" onClick={() => void decline(inv.id)}>
                  Decline
                </button>
              </li>
            )}
          </For>
        </ul>
      </section>
    </Show>
  );
};

export default InvitesInbox;
