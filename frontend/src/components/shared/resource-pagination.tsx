import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";

export const RESOURCE_PAGE_SIZE = 10;

type PageToken = number | "ellipsis-start" | "ellipsis-end";

function pageTokens(page: number, pageCount: number): PageToken[] {
  if (pageCount <= 7) {
    return Array.from({ length: pageCount }, (_, index) => index + 1);
  }

  const pages = new Set([1, pageCount, page - 1, page, page + 1]);
  const visible = [...pages]
    .filter((value) => value >= 1 && value <= pageCount)
    .sort((a, b) => a - b);
  const result: PageToken[] = [];

  visible.forEach((value, index) => {
    const previous = visible[index - 1];
    if (previous && value - previous > 1) {
      result.push(previous === 1 ? "ellipsis-start" : "ellipsis-end");
    }
    result.push(value);
  });

  return result;
}

export function ResourcePagination({
  page,
  pageSize = RESOURCE_PAGE_SIZE,
  total,
  onPageChange,
}: {
  page: number;
  pageSize?: number;
  total: number;
  onPageChange(page: number): void;
}) {
  const { t } = useI18n();
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  if (total === 0) return null;

  const start = (page - 1) * pageSize + 1;
  const end = Math.min(page * pageSize, total);

  function navigate(nextPage: number) {
    onPageChange(Math.min(pageCount, Math.max(1, nextPage)));
  }

  return (
    <div className="flex min-h-12 items-center justify-between gap-3 border-t bg-muted/20 px-3 py-2">
      <span className="text-[11px] text-muted-foreground">
        {t("pagination.summary", { start, end, total })}
      </span>
      <Pagination className="mx-0 w-auto" aria-label={t("pagination.label")}>
        <PaginationContent>
          <PaginationItem>
            <PaginationPrevious
              href="#"
              text={t("pagination.previous")}
              aria-label={t("pagination.previous")}
              aria-disabled={page === 1}
              tabIndex={page === 1 ? -1 : 0}
              className={cn(page === 1 && "pointer-events-none opacity-50")}
              onClick={(event) => {
                event.preventDefault();
                navigate(page - 1);
              }}
            />
          </PaginationItem>
          {pageTokens(page, pageCount).map((token) =>
            typeof token === "number" ? (
              <PaginationItem key={token}>
                <PaginationLink
                  href="#"
                  isActive={token === page}
                  aria-label={t("pagination.page", { page: token })}
                  onClick={(event) => {
                    event.preventDefault();
                    navigate(token);
                  }}
                >
                  {token}
                </PaginationLink>
              </PaginationItem>
            ) : (
              <PaginationItem key={token}>
                <PaginationEllipsis title={t("pagination.more")} />
              </PaginationItem>
            ),
          )}
          <PaginationItem>
            <PaginationNext
              href="#"
              text={t("pagination.next")}
              aria-label={t("pagination.next")}
              aria-disabled={page === pageCount}
              tabIndex={page === pageCount ? -1 : 0}
              className={cn(page === pageCount && "pointer-events-none opacity-50")}
              onClick={(event) => {
                event.preventDefault();
                navigate(page + 1);
              }}
            />
          </PaginationItem>
        </PaginationContent>
      </Pagination>
    </div>
  );
}
