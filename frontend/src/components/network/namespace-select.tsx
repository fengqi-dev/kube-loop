import { Field, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useI18n } from "@/i18n";

export function NamespaceSelect({
  value,
  namespaces,
  disabled,
  onChange,
}: {
  value: string;
  namespaces: string[];
  disabled?: boolean;
  onChange(value: string): void;
}) {
  const { t } = useI18n();
  return (
    <Field className="min-w-0 gap-1.5">
      <FieldLabel className="text-[10px] font-medium text-muted-foreground">
        {t("network.namespace")}
      </FieldLabel>
      <Select
        value={value || undefined}
        disabled={disabled || namespaces.length === 0}
        onValueChange={onChange}
      >
        <SelectTrigger className="h-9 w-full min-w-[140px]">
          <SelectValue placeholder={t("network.selectNamespace")} />
        </SelectTrigger>
        <SelectContent>
          {namespaces.map((item) => (
            <SelectItem key={item} value={item}>
              {item}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}
