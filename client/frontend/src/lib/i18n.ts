const messages: Record<string, string> = {
  "nav.sheetTitle": "导航",
  "nav.sheetText": "Endpoint 与 Conversation",
  "nav.toggleSidebar": "展开或收起侧栏",
};
export const t = (key: string) => messages[key] ?? key;
