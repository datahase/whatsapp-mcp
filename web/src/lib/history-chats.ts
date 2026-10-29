type Chat = { jid: string; name: string };

function named(chat: Chat): boolean {
  const name = chat.name.trim();
  return !!name && name !== chat.jid && name !== chat.jid.split("@")[0] &&
    !/^[+\d\s().-]+$/.test(name);
}

// Filtering never changes selection. Sort a copy so polling data stays intact.
export function filterChats<T extends Chat>(chats: T[], query: string): T[] {
  const needle = query.trim().normalize("NFKC").toLocaleLowerCase();
  const digits = /^[+\d\s().-]+$/.test(needle) ? needle.replace(/\D/g, "") : "";
  return chats.filter((c) =>
    `${c.name} ${c.jid}`.normalize("NFKC").toLocaleLowerCase().includes(needle) ||
    (!!digits && c.jid.split("@")[0].replace(/\D/g, "").includes(digits)),
  ).sort((a, b) => Number(named(b)) - Number(named(a)) ||
    (a.name || a.jid).localeCompare(b.name || b.jid, undefined, { numeric: true, sensitivity: "base" }) ||
    a.jid.localeCompare(b.jid));
}
