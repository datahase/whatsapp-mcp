export function formatHistoryDate(value?: string, time = true): string {
  if (!value) return "an unknown date";
  const date = new Date(value.replace(" ", "T"));
  if (!Number.isFinite(date.getTime())) return "an unknown date";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium", ...(time ? { timeStyle: "short" as const } : {}),
  }).format(date);
}

export function countLabel(count: number, noun: string): string {
  return `${count.toLocaleString()} ${noun}${count === 1 ? "" : "s"}`;
}

export function historyTranslationText(value: unknown): string | null {
  if (typeof value === "string") return value;
  if (value && typeof value === "object" && "text" in value && typeof value.text === "string") return value.text;
  return null;
}
