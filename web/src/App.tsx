import { Route, Router, type RouteSectionProps } from "@solidjs/router";
import type { Component } from "solid-js";

import Home from "./routes/Home";

// Layout shared by every page.
const Layout: Component<RouteSectionProps> = (props) => (
  <>
    <header class="topbar">
      <a href="/" class="brand">
        couchcast
      </a>
    </header>
    <main class="page">{props.children}</main>
  </>
);

// Routes are declared here so the whole URL space is visible in one place.
const App: Component = () => (
  <Router root={Layout}>
    <Route path="/" component={Home} />
  </Router>
);

export default App;
