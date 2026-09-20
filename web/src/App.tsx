import { Route, Router, type RouteSectionProps } from "@solidjs/router";
import type { Component } from "solid-js";

import Toasts from "~/components/Toasts";
import UserMenu from "~/components/UserMenu";
import Home from "~/routes/Home";
import Join from "~/routes/Join";
import Login from "~/routes/Login";
import Profile from "~/routes/Profile";
import NewRoom from "~/routes/NewRoom";
import Admin from "~/routes/Admin";
import Register from "~/routes/Register";
import Room from "~/routes/Room";
import RoomSettings from "~/routes/RoomSettings";

// Layout shared by every page.
const Layout: Component<RouteSectionProps> = (props) => (
  <>
    <header class="topbar">
      <a href="/" class="brand">
        couchcast
      </a>
      <UserMenu />
    </header>
    <main class="page wide">{props.children}</main>
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
