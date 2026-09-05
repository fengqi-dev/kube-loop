import { WindowControls } from "@/components/layout/window-controls";

export function AppHeader({ platform }: { platform?: string }) {
  return (
    <header className="app-titlebar window-drag" data-platform={platform}>
      <WindowControls platform={platform} />
      <span className="app-title">KubeLoop</span>
    </header>
  );
}
