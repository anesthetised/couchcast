import { Route, Router, type RouteSectionProps } from "@solidjs/router";
import type { Component } from "solid-js";

import UserMenu from "~/components/UserMenu";
import Home from "~/routes/Home";
import Login from "~/routes/Login";
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
  </>
);

// Routes are declared here so the whole URL space is visible in one place.
const App: Component = () => (
  <Router root={Layout}>
    <Route path="/" component={Home} />
    <Route path="/new" component={NewRoom} />
    <Route path="/login" component={Login} />
    <Route path="/register" component={Register} />
    <Route path="/r/:slug" component={Room} />
    <Route path="/r/:slug/settings" component={RoomSettings} />
    <Route path="/admin" component={Admin} />
  </Router>
);

export default App;
