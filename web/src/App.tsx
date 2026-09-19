import { Route, Router, type RouteSectionProps } from "@solidjs/router";
import type { Component } from "solid-js";

import UserMenu from "~/components/UserMenu";
import Home from "~/routes/Home";
import Login from "~/routes/Login";
import Register from "~/routes/Register";

// Layout shared by every page.
const Layout: Component<RouteSectionProps> = (props) => (
  <>
    <header class="topbar">
      <a href="/" class="brand">
        couchcast
      </a>
      <UserMenu />
    </header>
    <main class="page">{props.children}</main>
  </>
);

// Routes are declared here so the whole URL space is visible in one place.
const App: Component = () => (
  <Router root={Layout}>
    <Route path="/" component={Home} />
    <Route path="/login" component={Login} />
    <Route path="/register" component={Register} />
  </Router>
);

export default App;
