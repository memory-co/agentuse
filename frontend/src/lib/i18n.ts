import { useSyncExternalStore } from 'react';

// 中英文界面。中文里 daemon 叫「连接设备」,harness 叫「执行器」;agent、会话照旧。
// 选择记在 localStorage,没选过就跟浏览器语言。服务端返回的错误信息不翻译。
export type Locale = 'zh' | 'en';
export const locales: { value: Locale; label: string }[] = [{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }];

const zh = {
  'common.loading': '加载中…', 'common.cancel': '取消', 'common.ok': '确定', 'common.delete': '删除', 'common.copy': '复制', 'common.copyFailed': '复制失败，请手动选中', 'common.close': '关闭',
  'common.name': '名字', 'common.description': '说明', 'common.user': '用户', 'common.admin': '管理员', 'common.normalUser': '普通用户', 'common.create': '创建', 'common.new': '新建',
  'nav.new': '新会话', 'nav.agents': 'Agent', 'nav.daemons': '连接设备', 'nav.harnesses': '执行器', 'nav.users': '用户',
  'nav.sessions': '会话', 'nav.sessionsFailed': '会话加载失败', 'nav.noSessions': '还没有会话', 'nav.logout': '退出', 'nav.language': 'English',
  'nav.sheetTitle': '导航', 'nav.sheetText': '侧栏导航', 'nav.toggleSidebar': '展开或收起侧栏',
  'nav.main': '主导航', 'nav.openNav': '打开导航', 'nav.closeNav': '关闭导航',
  'actAs.label': '身份', 'actAs.self': '{name}(管理员，看全部)', 'actAs.acting': '代为 {id}',
  'login.title': '登录 agentuse', 'login.text': '输入登录令牌(ut_ 开头)。管理员令牌在服务第一次启动时打印在日志里；其他用户的令牌由管理员创建。', 'login.token': '登录令牌', 'login.invalidLink': '登录链接无效', 'login.submit': '登录',
  'session.status.idle': '已回复', 'session.status.pending': '排队中', 'session.status.running': '运行中',
  'session.send': '发送', 'session.ask': '要 agent 做什么?', 'session.askText': '会话跑在 agent 此刻的运行时上(哪台连接设备上的哪个执行器);之后一直在同一台上续接。',
  'session.taskPlaceholder': '描述任务，回车发送', 'session.agentPlaceholder': 'agent id,别人的也行',
  'session.noAgents': '还没有自己的 agent,先去左边「Agent」建一个，或者直接填别人的 agent id。',
  'session.metaHarness': '执行器 {harness}', 'session.metaOnBehalf': '{by} 发起，跑在 {owner} 的设备上', 'session.metaDaemon': '连接设备 {id}', 'session.metaHarnessSession': '执行器会话 {id}',
  'session.deleteTitle': '删除这个会话?', 'session.deleteText': '只删 agentuse 里的记录。', 'session.delete': '删除会话',
  'session.replyPlaceholder': '接着说，回车发送', 'session.busyPlaceholder': 'agent 回复完才能接着说', 'session.daemonGone': '原来的连接设备已删除，这个会话不能继续。',
  'session.progress': '过程 {n} 条', 'session.failed': '失败',
  'agents.newTitle': '新建 agent', 'agents.newText': '给 agent 起个 id 和好懂的名字，选它的运行时：哪台连接设备上的哪个执行器。id 全局唯一，建好后不能改。以后可以随时切换运行时(连接设备和执行器一起换),agent 不变;已有的会话还在原来的运行时上，切换之后新建的会话才用新的。',
  'agents.idPlaceholder': 'id,如 code-helper', 'agents.namePlaceholder': '名字，如 写代码助手', 'agents.pickRuntime': '选择运行时(设备 · 执行器)', 'agents.listTitle': 'Agent', 'agents.runtime': '运行时',
  'agents.deleteTitle': '删除 agent {name}?', 'agents.deleteText': '用过它的会话保留。', 'agents.empty': '还没有 agent',
  'daemons.status.pending': '待注册', 'daemons.status.online': '在线', 'daemons.status.offline': '离线',
  'daemons.addTitle': '添加连接设备', 'daemons.addText': '在要跑执行器的电脑上执行生成的安装命令，就能把它接进来。', 'daemons.namePlaceholder': '名字(可选，默认用主机名)', 'daemons.add': '添加连接设备',
  'daemons.empty': '还没有连接设备，先添加一台。', 'daemons.enabled': '已开启', 'daemons.disabled': '已关闭', 'daemons.metaUser': '用户 {id}', 'daemons.metaSeen': '最近 {at}', 'daemons.unnamed': '(未命名)',
  'daemons.resetTitle': '重新生成安装命令?', 'daemons.resetText': '这台电脑现有的密钥会立即失效，需要重新跑安装命令。', 'daemons.reset': '重新生成安装命令',
  'daemons.deleteTitle': '删除这台连接设备?', 'daemons.deleteText': '它会断开，绑定在它上面的会话不能再继续。',
  'daemons.runSetup': '在要接入的电脑上运行(令牌只能用一次):', 'daemons.pickHarnesses': '选择这台电脑上开启哪些执行器:', 'daemons.notDetected': ' · 本机未检测到',
  'daemons.noHarnesses': '这台电脑还没上报可用的执行器(连上后会自动检测 claude、codex 等)。',
  'harnesses.newTitle': '注册自定义执行器', 'harnesses.newText1': '自己写的执行器：连接设备把 ', 'harnesses.newText2': ' 以 JSON 写进 stdin,程序往 stdout 按行输出 JSON(', 'harnesses.newText3': ',session_id 下次通过 resume 带回来)。注册后在连接设备的 ', 'harnesses.newText4': ' 里配置本机启动命令。',
  'harnesses.namePlaceholder': '名字，如 my-harness', 'harnesses.displayName': '显示名', 'harnesses.register': '注册', 'harnesses.listTitle': '全部执行器', 'harnesses.kind': '类型',
  'harnesses.builtin': '内置', 'harnesses.custom': '自定义', 'harnesses.deleteTitle': '删除执行器 {name}?',
  'users.newTitle': '新建用户', 'users.idPlaceholder': '用户 id(可选，填你们系统的 user id)', 'users.namePlaceholder': '用户名', 'users.created1': '已创建 ', 'users.created2': '。把下面的登录链接发给 TA(只显示这一次):',
  'users.listTitle': '用户', 'users.name': '用户名', 'users.role': '角色', 'users.createdAt': '创建',
};
type Key = keyof typeof zh;

const en: Record<Key, string> = {
  'common.loading': 'Loading…', 'common.cancel': 'Cancel', 'common.ok': 'OK', 'common.delete': 'Delete', 'common.copy': 'Copy', 'common.copyFailed': 'Copy failed, please select it manually', 'common.close': 'Close',
  'common.name': 'Name', 'common.description': 'Description', 'common.user': 'User', 'common.admin': 'Admin', 'common.normalUser': 'User', 'common.create': 'Create', 'common.new': 'Create',
  'nav.new': 'New session', 'nav.agents': 'Agents', 'nav.daemons': 'Daemons', 'nav.harnesses': 'Harnesses', 'nav.users': 'Users',
  'nav.sessions': 'Sessions', 'nav.sessionsFailed': 'Failed to load sessions', 'nav.noSessions': 'No sessions yet', 'nav.logout': 'Log out', 'nav.language': '中文',
  'nav.sheetTitle': 'Navigation', 'nav.sheetText': 'Sidebar navigation', 'nav.toggleSidebar': 'Toggle sidebar',
  'nav.main': 'Main navigation', 'nav.openNav': 'Open navigation', 'nav.closeNav': 'Close navigation',
  'actAs.label': 'Acting as', 'actAs.self': '{name} (admin, see everything)', 'actAs.acting': 'Acting as {id}',
  'login.title': 'Sign in to agentuse', 'login.text': 'Enter your login token (starts with ut_). The admin token is printed in the log when the server first starts; other users get theirs from the admin.', 'login.token': 'Login token', 'login.invalidLink': 'Invalid login link', 'login.submit': 'Sign in',
  'session.status.idle': 'Replied', 'session.status.pending': 'Queued', 'session.status.running': 'Running',
  'session.send': 'Send', 'session.ask': 'What should the agent do?', 'session.askText': "The session runs on the agent's current runtime (a harness on one of its owner's daemons), and keeps resuming on that same machine.",
  'session.taskPlaceholder': 'Describe the task, Enter to send', 'session.agentPlaceholder': "agent id, anyone's works",
  'session.noAgents': "You have no agents yet. Create one under “Agents” on the left, or enter someone else's agent id.",
  'session.metaHarness': 'harness {harness}', 'session.metaOnBehalf': "started by {by}, runs on {owner}'s machine", 'session.metaDaemon': 'daemon {id}', 'session.metaHarnessSession': 'harness session {id}',
  'session.deleteTitle': 'Delete this session?', 'session.deleteText': 'Only the record in agentuse is deleted.', 'session.delete': 'Delete session',
  'session.replyPlaceholder': 'Reply, Enter to send', 'session.busyPlaceholder': 'You can reply once the agent has answered', 'session.daemonGone': 'The original daemon was deleted, so this session cannot continue.',
  'session.progress': '{n} progress updates', 'session.failed': 'Failed',
  'agents.newTitle': 'New agent', 'agents.newText': 'Give the agent an id and a readable name, and pick its runtime: a harness on one of your daemons. The id is globally unique and cannot be changed. You can switch the runtime (daemon and harness together) any time and the agent stays; existing sessions keep the old runtime, new sessions use the new one.',
  'agents.idPlaceholder': 'id, e.g. code-helper', 'agents.namePlaceholder': 'Name, e.g. Code helper', 'agents.pickRuntime': 'Pick a runtime (daemon · harness)', 'agents.listTitle': 'Agents', 'agents.runtime': 'Runtime',
  'agents.deleteTitle': 'Delete agent {name}?', 'agents.deleteText': 'Sessions that used it are kept.', 'agents.empty': 'No agents yet',
  'daemons.status.pending': 'Not registered', 'daemons.status.online': 'Online', 'daemons.status.offline': 'Offline',
  'daemons.addTitle': 'Add a daemon', 'daemons.addText': 'Run the generated install command on the computer that should run harnesses to register it as a daemon.', 'daemons.namePlaceholder': 'Name (optional, defaults to hostname)', 'daemons.add': 'Add daemon',
  'daemons.empty': 'No daemons yet. Add one first.', 'daemons.enabled': 'Enabled', 'daemons.disabled': 'Disabled', 'daemons.metaUser': 'user {id}', 'daemons.metaSeen': 'last seen {at}', 'daemons.unnamed': '(unnamed)',
  'daemons.resetTitle': 'Regenerate the install command?', 'daemons.resetText': "This computer's current key stops working immediately and the install command must be run again.", 'daemons.reset': 'Regenerate install command',
  'daemons.deleteTitle': 'Delete this daemon?', 'daemons.deleteText': 'It disconnects, and sessions bound to it cannot continue.',
  'daemons.runSetup': 'Run this on the computer to connect (the token works once):', 'daemons.pickHarnesses': 'Choose which harnesses to enable on this computer:', 'daemons.notDetected': ' · not detected on this machine',
  'daemons.noHarnesses': 'This computer has not reported any harnesses yet (claude, codex and others are detected once it connects).',
  'harnesses.newTitle': 'Register a custom harness', 'harnesses.newText1': 'Your own harness: the daemon writes ', 'harnesses.newText2': ' as JSON to stdin, and the program prints JSON lines to stdout (', 'harnesses.newText3': '; session_id comes back via resume next time). After registering, set the local launch command in the daemon\'s ', 'harnesses.newText4': '.',
  'harnesses.namePlaceholder': 'Name, e.g. my-harness', 'harnesses.displayName': 'Display name', 'harnesses.register': 'Register', 'harnesses.listTitle': 'All harnesses', 'harnesses.kind': 'Kind',
  'harnesses.builtin': 'Built-in', 'harnesses.custom': 'Custom', 'harnesses.deleteTitle': 'Delete harness {name}?',
  'users.newTitle': 'New user', 'users.idPlaceholder': "User id (optional, your system's user id)", 'users.namePlaceholder': 'Username', 'users.created1': 'Created ', 'users.created2': '. Send them this login link (shown only once):',
  'users.listTitle': 'Users', 'users.name': 'Username', 'users.role': 'Role', 'users.createdAt': 'Created',
};

const KEY = 'agentuse.locale';
const listeners = new Set<() => void>();
let current: Locale = (() => {
  try { const v = localStorage.getItem(KEY); if (v === 'zh' || v === 'en') return v; } catch { /* 无痕模式 */ }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en';
})();
const applyLang = () => { document.documentElement.lang = current === 'zh' ? 'zh-CN' : 'en'; };
applyLang();

export const getLocale = () => current;
export function setLocale(l: Locale) {
  if (l === current) return;
  current = l;
  try { localStorage.setItem(KEY, l); } catch { /* 无痕模式 */ }
  applyLang();
  listeners.forEach(f => f());
}

/** 按当前语言取文案,{name} 这样的占位换成 vars 里的值。 */
export function t(key: Key, vars?: Record<string, string | number>) {
  const s = (current === 'zh' ? zh : en)[key] ?? key;
  return vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s;
}

/** 组件里用：语言一换就重画。 */
export function useT() {
  useSyncExternalStore(f => { listeners.add(f); return () => listeners.delete(f); }, getLocale);
  return t;
}
export const useLocale = () => useSyncExternalStore(f => { listeners.add(f); return () => listeners.delete(f); }, getLocale);
