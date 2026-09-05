import { Minus, X, Square, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { WindowHide, WindowMinimise, WindowToggleMaximise, WindowIsMaximised } from "../../../../../wailsjs/runtime/runtime";

export function WindowControls({ platform }: { platform?: string }) {
  const { t } = useI18n();
  const [maximised, setMaximised] = useState(false);
  useEffect(() => {
    let active = true;
    const sync = () => { if (window.runtime) void WindowIsMaximised().then(value => { if (active) setMaximised(value); }).catch(() => {}); };
    sync();
    window.addEventListener("resize", sync);
    return () => { active = false; window.removeEventListener("resize", sync); };
  }, []);
  // macOS supplies native traffic lights with the rounded window frame.
  if (platform === "darwin") return null;
  return (
    <div className="window-no-drag standard-window-controls flex items-center overflow-hidden">
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={t("window.minimise")}
        title={t("window.minimise")}
        onClick={() => WindowMinimise()}
        className="rounded-none"
      >
        <Minus strokeWidth={1.8} />
      </Button>
      <Button type="button" variant="ghost" size="icon" className="rounded-none border-l"
        aria-label={t("window.maximise")} title={t("window.maximise")}
        onClick={() => WindowToggleMaximise()}>
        {maximised ? <Copy strokeWidth={1.8} /> : <Square strokeWidth={1.8} />}
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={t("window.close")}
        title={t("window.close")}
        onClick={() => WindowHide()}
        className="rounded-none border-l hover:bg-destructive hover:text-white"
      >
        <X strokeWidth={1.8} />
      </Button>
    </div>
  );
}
