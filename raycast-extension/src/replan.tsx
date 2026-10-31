import { showHUD, showToast, Toast } from "@raycast/api";
import { triggerReplan } from "./api";

export default async function Replan() {
  try {
    await showToast({ style: Toast.Style.Animated, title: "Triggering replan…" });
    await triggerReplan();
    await showHUD("Replan triggered — schedule will update shortly");
  } catch (err) {
    await showToast({
      style: Toast.Style.Failure,
      title: "Replan failed",
      message: err instanceof Error ? err.message : String(err),
    });
  }
}
