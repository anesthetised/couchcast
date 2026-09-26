import { Route, Router, type RouteSectionProps } from "@solidjs/router";
import { lazy, type Component } from "solid-js";

import Toasts from "~/components/Toasts";
import UserMenu from "~/components/UserMenu";
import Home from "~/routes/Home";
import Login from "~/routes/Login";
import Register from "~/routes/Register";

// The directory and the auth pages ship with the app; everything else
// loads on first visit — the room brings the player (Shaka) with it.
const Room = lazy(() => import("~/routes/Room"));
const RoomSettings = lazy(() => import("~/routes/RoomSettings"));
const NewRoom = lazy(() => import("~/routes/NewRoom"));
const Join = lazy(() => import("~/routes/Join"));
const Profile = lazy(() => import("~/routes/Profile"));
const Admin = lazy(() => import("~/routes/Admin"));

// Layout shared by every page.
const Layout: Component<RouteSectionProps> = (props) => (
  <>
    {/* Keyboard users jump past the header straight to the page. The
        router would treat #main as a route, so the jump is done here. */}
    <a
      class="skip-link"
      href="#main"
      rel="external"
      onClick={(e) => {
        e.preventDefault();
        document.getElementById("main")?.focus();
      }}
    >
      Skip to content
    </a>
    <header class="topbar">
      <a href="/" class="brand">
        couchcast
      </a>
      <UserMenu />
    </header>
    <main id="main" class="page wide" tabindex="-1">
      {props.children}
    </main>
    <Toasts />
  </>
);

// Routes are declared here so the whole URL space is visible in one place.
const App: Component = () => (
  <Router root={Layout}>
    <Route path="/" component={Home} />
    <Route path="/new" component={NewRoom} />
    <Route path="/login" component={Login} />
    <Route path="/me" component={Profile} />
    <Route path="/join/:token" component={Join} />
    <Route path="/register" component={Register} />
    <Route path="/r/:slug" component={Room} />
    <Route path="/r/:slug/settings" component={RoomSettings} />
    <Route path="/admin" component={Admin} />
  </Router>
);

export default App;
