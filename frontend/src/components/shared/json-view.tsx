import { useMemo, useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

function isPlainObject(value: unknown): value is Record<string, JsonValue> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function preview(value: JsonValue): string {
  if (value === null) return "null";
  if (typeof value === "string") return `"${value.length > 24 ? `${value.slice(0, 24)}…` : value}"`;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `Array(${value.length})`;
  return `Object(${Object.keys(value).length})`;
}

function KeyLabel({ name, isIndex }: { name?: string; isIndex?: boolean }) {
  if (name === undefined) return null;
  return (
    <>
      {isIndex ? (
        <span className="text-muted-foreground">{name}</span>
      ) : (
        <span className="text-sky-700 dark:text-sky-300">"{name}"</span>
      )}
      <span className="text-muted-foreground">: </span>
    </>
  );
}

function JsonNode({
  name,
  value,
  defaultOpen = true,
  isIndex,
}: {
  name?: string;
  value: JsonValue;
  defaultOpen?: boolean;
  isIndex?: boolean;
}) {
  const isExpandable = Array.isArray(value) || isPlainObject(value);
  const [open, setOpen] = useState(defaultOpen);

  if (!isExpandable) {
    return (
      <div className="flex flex-wrap gap-x-0 font-mono text-[11px] leading-5">
        <KeyLabel name={name} isIndex={isIndex} />
        <Primitive value={value} />
      </div>
    );
  }

  const entries = Array.isArray(value)
    ? value.map((item, index) => [String(index), item] as const)
    : Object.entries(value);
  const openBracket = Array.isArray(value) ? "[" : "{";
  const closeBracket = Array.isArray(value) ? "]" : "}";

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="font-mono text-[11px] leading-5"
    >
      <CollapsibleTrigger
        className="inline-flex max-w-full items-start gap-1 rounded-sm text-left hover:bg-muted/60"
      >
        <span className="mt-0.5 shrink-0 text-muted-foreground">
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </span>
        <span className="min-w-0">
          <KeyLabel name={name} isIndex={isIndex} />
          <span className="text-muted-foreground">{openBracket}</span>
          {!open ? (
            <>
              <span className="ml-1 text-muted-foreground/80">{preview(value)}</span>
              <span className="text-muted-foreground">{closeBracket}</span>
            </>
          ) : null}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="ml-3 border-l border-border/70 pl-3">
          {entries.map(([key, child]) => (
            <JsonNode
              key={key}
              name={key}
              value={child}
              isIndex={Array.isArray(value)}
            />
          ))}
          <div className="text-muted-foreground">{closeBracket}</div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function Primitive({ value }: { value: Exclude<JsonValue, JsonValue[] | { [key: string]: JsonValue }> }) {
  if (value === null) {
    return <span className="text-violet-700 dark:text-violet-300">null</span>;
  }
  if (typeof value === "boolean") {
    return <span className="text-amber-700 dark:text-amber-300">{String(value)}</span>;
  }
  if (typeof value === "number") {
    return <span className="text-emerald-700 dark:text-emerald-300">{value}</span>;
  }
  return <span className="text-rose-700 dark:text-rose-300">"{value}"</span>;
}

export function JsonView({
  value,
  className,
}: {
  value: string | unknown;
  className?: string;
}) {
  const parsed = useMemo(() => {
    if (typeof value !== "string") {
      return { ok: true as const, data: value as JsonValue };
    }
    try {
      return { ok: true as const, data: JSON.parse(value) as JsonValue };
    } catch {
      return { ok: false as const, raw: value };
    }
  }, [value]);

  let body: ReactNode;
  if (!parsed.ok) {
    body = (
      <pre className="whitespace-pre-wrap break-all font-mono text-[11px] leading-5 text-foreground">
        {parsed.raw}
      </pre>
    );
  } else {
    body = <JsonNode value={parsed.data} />;
  }

  return <div className={cn("p-3", className)}>{body}</div>;
}
