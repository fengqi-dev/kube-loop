import { Minus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { WindowHide, WindowMinimise } from "../../../wailsjs/runtime/runtime";

export function WindowControls() {
  const { t } = useI18n();
  return (
    <div className="window-no-drag ml-1 flex items-center overflow-hidden rounded-lg border bg-card">
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
