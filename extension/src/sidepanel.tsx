import { Component, StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";

/** Last line of defence: a broken screen offers a reload instead of a blank panel. */
class Boundary extends Component<{ children: ReactNode }, { broken: boolean }> {
  state = { broken: false };
  static getDerivedStateFromError() {
    return { broken: true };
  }
  render() {
    if (!this.state.broken) return this.props.children;
    return (
      <div className="panel">
        <main className="body">
          <h1>Something went wrong in the panel</h1>
          <p>Your translations are kept by the helper. Reload the panel to continue.</p>
          <button className="primary push" onClick={() => location.reload()}>
            Reload
          </button>
        </main>
      </div>
    );
  }
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Boundary>
      <App />
    </Boundary>
  </StrictMode>,
);
