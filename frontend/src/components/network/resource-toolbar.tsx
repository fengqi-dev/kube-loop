import { RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useI18n } from "@/i18n";
import type { ReactNode } from "react";

export const ALL_NAMESPACES = "*";

export function ResourceToolbar({
  namespaces,
  namespace,
  onNamespaceChange,
  query,
  onQueryChange,
  searchPlaceholder,
  count,
  loading,
  disabled,
  onRefresh,
  actions,
  allowAllNamespaces = true,
}: {
  namespaces: string[];
  namespace: string;
  onNamespaceChange(value: string): void;
  query: string;
  onQueryChange(value: string): void;
  searchPlaceholder: string;
  count: number;
  loading: boolean;
  disabled: boolean;
  onRefresh(): void;
  actions?: ReactNode;
  allowAllNamespaces?: boolean;
}) {
  const { t } = useI18n();

  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <Select value={namespace} onValueChange={onNamespaceChange} disabled={disabled}>
        <SelectTrigger className="h-8 w-[180px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {allowAllNamespaces ? (
            <SelectItem value={ALL_NAMESPACES}>{t("network.allNamespaces")}</SelectItem>
          ) : null}
          {namespaces.map((item) => (
            <SelectItem key={item} value={item}>
              {item}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <div className="relative min-w-[220px] flex-1">
        <Search
          size={14}
          className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          className="h-8 w-full rounded-lg border border-input bg-transparent pr-3 pl-8 text-sm outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30"
          value={query}
          disabled={disabled}
          onChange={(event) => onQueryChange(event.target.value)}
          placeholder={searchPlaceholder}
        />
      </div>

      <span className="text-[11px] text-muted-foreground tabular-nums">
        {t("network.itemCount", { count })}
      </span>

      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled || loading}
        onClick={onRefresh}
      >
        {loading ? (
          <Spinner data-icon="inline-start" />
        ) : (
          <RefreshCw data-icon="inline-start" />
        )}
        {t("network.refresh")}
      </Button>

      {actions}
    </div>
  );
}
