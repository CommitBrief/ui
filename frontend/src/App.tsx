// SPDX-License-Identifier: GPL-3.0-or-later
import { ToastProvider } from "./components";
import { Shell } from "./app/Shell";

function App() {
  return (
    <ToastProvider>
      <Shell />
    </ToastProvider>
  );
}

export default App;
