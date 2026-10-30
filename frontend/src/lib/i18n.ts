import { useSyncExternalStore } from 'react';

// 中英文界面。中文里 daemon 叫「连接设备」,harness 叫「执行器」;agent、会话照旧。
// 选择记在 localStorage,没选过就跟浏览器语言。服务端返回的错误信息不翻译。
export type Locale = 'zh' | 'en';
export const locales: { value: Locale; label: string }[] = [{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }];

const zh = {
  'common.loading': '加载中…', 'common.cancel': '取消', 'common.ok': '确定', 'common.delete': '删除', 'common.copy': '复制', 'common.copyFailed': '复制失败，请手动选中', 'common.close': '关闭',
  'common.name': '名字', 'common.description': '说明', 'common.user': '用户', 'common.admin': '管理员', 'common.normalUser': '普通用户', 'common.create': '创建', 'common.new': '新建',
  'nav.new': '新会话', 'nav.agents': 'Agent', 'nav.daemons': '连接设备', 'nav.harnesses': '执行器', 'nav.users': '用户',
  'nav.sessions': '会话', 'nav.sessionsFailed': '会话加载失败', 'nav.noSessions': '让 agent 帮你干点活', 'nav.logout': '退出', 'nav.language': 'English',
  'nav.sheetTitle': '导航', 'nav.sheetText': '侧栏导航', 'nav.toggleSidebar': '展开或收起侧栏',
  'nav.main': '主导航', 'nav.openNav': '打开导航', 'nav.closeNav': '关闭导航',
  'actAs.label': '身份', 'actAs.self': '{name}(管理员，看全部)', 'actAs.acting': '代为 {id}', 'actAs.switch': '切换身份', 'actAs.search': '搜索用户…', 'actAs.empty': '没有匹配的用户',
  'login.title': '登录 agentuse', 'login.text': '输入登录令牌(ut_ 开头)。管理员令牌在服务第一次启动时打印在日志里；其他用户的令牌由管理员创建。', 'login.token': '登录令牌', 'login.invalidLink': '登录链接无效', 'login.submit': '登录',
  'session.status.idle': '已回复', 'session.status.pending': '排队中', 'session.status.running': '运行中', 'session.status.waiting': '等你回答', 'session.status.resolved': '已完成',
  'session.resolve': '标记完成', 'session.reopen': '取消完成', 'session.resolved': '已完成 ({n})', 'session.threadReplied': '线程 {title} 回复了',
  'nav.tabSessions': '会话', 'nav.tabWorks': '工作', 'nav.newWork': '新建工作', 'nav.noWorks': '让协调者帮你拆活、开线程', 'nav.worksFailed': '工作加载失败', 'nav.noThreads': '还没有线程',
  'work.newTitle': '新建工作', 'work.newText': '工作里你只跟协调者说话，它会开线程去干具体的活，线程做完结果回到它这里。说明会带给协调者和每个线程。',
  'work.namePlaceholder': '名字，如 重构 daemon', 'work.agentLabel': '协调者用哪个 agent', 'work.pickAgent': '选一个 agent', 'work.noAgents': '还没有自己的 agent:先去左边「Agent」建一个。', 'work.instructions': '说明', 'work.instructionsPlaceholder': '背景、约定、要注意的事(可空)',
  'work.instructionsHint': '这个工作里的协调者和每个线程都会带上，改了下一条消息就生效。', 'work.save': '保存', 'work.saved': '已保存',
  'work.threads': '线程 {n}',
  'work.deleteTitle': '删除工作 {name}?', 'work.deleteText': '协调者和所有线程会一起删掉。', 'work.delete': '删除工作', 'work.coordinatorPlaceholder': '跟协调者说要做什么，回车发送',
  'mention.choose': '@ 选择 agent', 'mention.search': '搜索名字或输入 agent id', 'mention.remove': '移除目标', 'mention.noMatch': '没有匹配项', 'mention.targets': '发送给 {n} 个 agent', 'mention.note': '未 @ agent，仅记入会话',
  'mention.cancel': '取消执行', 'mention.upgrade': '升级为多 agent 会话', 'mention.shared': '共享 session：@ 选择本次执行者', 'mention.pending': '排队', 'mention.running': '执行中', 'mention.waiting': '等回答', 'mention.succeeded': '已完成', 'mention.failed': '失败', 'mention.cancelled': '已取消', 'mention.delivered': '已送达', 'mention.steer': '插话',
  'attachment.title': '附件', 'attachment.upload': '上传文件', 'attachment.download': '下载', 'attachment.delete': '删除附件？',
  'attachment.empty': '还没有附件', 'attachment.loading': '正在加载附件…', 'attachment.nextTurn': '新上传的文件下一轮可用；需要立即使用时请插话说明。',
  'attachment.selected': '已选 {n} 个文件', 'attachment.created': '附件上传失败时会保留空会话和已上传文件，可重试或进入会话处理。', 'attachment.open': '打开已创建的会话',
  'session.send': '发送', 'session.ask': '要 agent 做什么?', 'session.askText': '会话跑在 agent 此刻的运行时上(哪台连接设备上的哪个执行器);之后一直在同一台上续接。',
  'session.taskPlaceholder': '描述任务，回车发送', 'session.agentPlaceholder': 'agent id,别人的也行',
  'session.noAgents': '还没有自己的 agent,先去左边「Agent」建一个，或者直接填别人的 agent id。',
  'session.metaHarness': '执行器 {harness}', 'session.metaOnBehalf': '{by} 发起，跑在 {owner} 的设备上', 'session.metaDaemon': '连接设备 {id}', 'session.metaHarnessSession': '执行器会话 {id}',
  'session.deleteTitle': '删除这个会话?', 'session.deleteText': '只删 agentuse 里的记录。', 'session.delete': '删除会话',
  'session.replyPlaceholder': '接着说，回车发送', 'session.busyPlaceholder': '补充说明，会在它下一步之前送达', 'session.steerDelivered': '已送达', 'session.steerPending': '等待送达', 'session.daemonGone': '原来的连接设备已删除，这个会话不能继续。',
  'session.daemonOffline': '连接设备离线，上线后才会开始。', 'session.progress': '过程 {n} 条', 'session.failed': '失败',
  'question.other': '其他，自己写', 'question.submit': '回答', 'question.sending': '已回答，等待送达', 'question.answered': '已回答', 'question.expired': '没有回答，已过期', 'question.ask': '问', 'question.answer': '答',
  'agents.newTitle': '新建 agent', 'agents.newText': '给 agent 起个 id 和好懂的名字，选它的运行时：哪台连接设备上的哪个执行器。id 全局唯一，建好后不能改。以后可以随时切换运行时(连接设备和执行器一起换),agent 不变;已有的会话还在原来的运行时上，切换之后新建的会话才用新的。',
  'agents.idPlaceholder': 'id,如 code-helper', 'agents.namePlaceholder': '名字，如 写代码助手', 'agents.pickRuntime': '选择运行时(设备 · 执行器)', 'agents.listTitle': 'Agent', 'agents.runtime': '运行时',
  'agents.deleteTitle': '删除 agent {name}?', 'agents.deleteText': '用过它的会话保留。', 'agents.empty': '还没有 agent',
  'agents.offline': '(离线)', 'agents.unavailable': '(执行器未开启)', 'agents.unavailableHint': '这个执行器已关闭或本机检测不到，新会话会直接报错，请换一个运行时。', 'agents.noRuntimes': '没有可用的运行时：先到「连接设备」页开启执行器。',
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
  'nav.sessions': 'Sessions', 'nav.sessionsFailed': 'Failed to load sessions', 'nav.noSessions': 'Ask an agent to do something', 'nav.logout': 'Log out', 'nav.language': '中文',
  'nav.sheetTitle': 'Navigation', 'nav.sheetText': 'Sidebar navigation', 'nav.toggleSidebar': 'Toggle sidebar',
  'nav.main': 'Main navigation', 'nav.openNav': 'Open navigation', 'nav.closeNav': 'Close navigation',
  'actAs.label': 'Acting as', 'actAs.self': '{name} (admin, see everything)', 'actAs.acting': 'Acting as {id}', 'actAs.switch': 'Switch identity', 'actAs.search': 'Search users…', 'actAs.empty': 'No matching users',
  'login.title': 'Sign in to agentuse', 'login.text': 'Enter your login token (starts with ut_). The admin token is printed in the log when the server first starts; other users get theirs from the admin.', 'login.token': 'Login token', 'login.invalidLink': 'Invalid login link', 'login.submit': 'Sign in',
  'session.status.idle': 'Replied', 'session.status.pending': 'Queued', 'session.status.running': 'Running', 'session.status.waiting': 'Needs your answer', 'session.status.resolved': 'Done',
  'session.resolve': 'Mark done', 'session.reopen': 'Reopen', 'session.resolved': 'Done ({n})', 'session.threadReplied': 'Thread {title} replied',
  'nav.tabSessions': 'Sessions', 'nav.tabWorks': 'Works', 'nav.newWork': 'New work', 'nav.noWorks': 'Let a coordinator split the job into threads', 'nav.worksFailed': 'Failed to load works', 'nav.noThreads': 'No threads yet',
  'work.newTitle': 'New work', 'work.newText': 'In a work you only talk to the coordinator; it starts threads for the actual tasks, and their results come back to it. The instructions go to the coordinator and every thread.',
  'work.namePlaceholder': 'Name, e.g. Refactor daemon', 'work.agentLabel': 'Agent for the coordinator', 'work.pickAgent': 'Pick an agent', 'work.noAgents': 'You have no agents yet. Create one under “Agents” on the left first.', 'work.instructions': 'Instructions', 'work.instructionsPlaceholder': 'Background, conventions, things to watch out for (optional)',
  'work.instructionsHint': 'The coordinator and every thread in this work get these; changes apply from the next message.', 'work.save': 'Save', 'work.saved': 'Saved',
  'work.threads': '{n} threads',
  'work.deleteTitle': 'Delete work {name}?', 'work.deleteText': 'The coordinator and all threads are deleted with it.', 'work.delete': 'Delete work', 'work.coordinatorPlaceholder': 'Tell the coordinator what to do, Enter to send',
  'mention.choose': '@ Choose agents', 'mention.search': 'Search name or agent id', 'mention.remove': 'Remove target', 'mention.noMatch': 'No matches', 'mention.targets': 'Send to {n} agents', 'mention.note': 'No @ target; save as a note',
  'mention.cancel': 'Cancel run', 'mention.upgrade': 'Upgrade to multi-agent session', 'mention.shared': 'Shared session: @ agents for this message', 'mention.pending': 'Queued', 'mention.running': 'Running', 'mention.waiting': 'Waiting for answer', 'mention.succeeded': 'Completed', 'mention.failed': 'Failed', 'mention.cancelled': 'Cancelled', 'mention.delivered': 'Delivered', 'mention.steer': 'Follow-up',
  'attachment.title': 'Attachments', 'attachment.upload': 'Upload files', 'attachment.download': 'Download', 'attachment.delete': 'Delete attachment?',
  'attachment.empty': 'No attachments yet', 'attachment.loading': 'Loading attachments…', 'attachment.nextTurn': 'New uploads are available next turn. Send a follow-up if needed now.',
  'attachment.selected': '{n} files selected', 'attachment.created': 'If upload fails, the empty session and uploaded files are kept. Retry or open the session.', 'attachment.open': 'Open created session',
  'session.send': 'Send', 'session.ask': 'What should the agent do?', 'session.askText': "The session runs on the agent's current runtime (a harness on one of its owner's daemons), and keeps resuming on that same machine.",
  'session.taskPlaceholder': 'Describe the task, Enter to send', 'session.agentPlaceholder': "agent id, anyone's works",
  'session.noAgents': "You have no agents yet. Create one under “Agents” on the left, or enter someone else's agent id.",
  'session.metaHarness': 'harness {harness}', 'session.metaOnBehalf': "started by {by}, runs on {owner}'s machine", 'session.metaDaemon': 'daemon {id}', 'session.metaHarnessSession': 'harness session {id}',
  'session.deleteTitle': 'Delete this session?', 'session.deleteText': 'Only the record in agentuse is deleted.', 'session.delete': 'Delete session',
  'session.replyPlaceholder': 'Reply, Enter to send', 'session.busyPlaceholder': 'Add a note, delivered before its next step', 'session.steerDelivered': 'Delivered', 'session.steerPending': 'Waiting to deliver', 'session.daemonGone': 'The original daemon was deleted, so this session cannot continue.',
  'session.daemonOffline': 'The daemon is offline; this starts once it comes back online.', 'session.progress': '{n} progress updates', 'session.failed': 'Failed',
  'question.other': 'Other, write your own', 'question.submit': 'Answer', 'question.sending': 'Answered, waiting to deliver', 'question.answered': 'Answered', 'question.expired': 'Not answered, expired', 'question.ask': 'Q', 'question.answer': 'A',
  'agents.newTitle': 'New agent', 'agents.newText': 'Give the agent an id and a readable name, and pick its runtime: a harness on one of your daemons. The id is globally unique and cannot be changed. You can switch the runtime (daemon and harness together) any time and the agent stays; existing sessions keep the old runtime, new sessions use the new one.',
  'agents.idPlaceholder': 'id, e.g. code-helper', 'agents.namePlaceholder': 'Name, e.g. Code helper', 'agents.pickRuntime': 'Pick a runtime (daemon · harness)', 'agents.listTitle': 'Agents', 'agents.runtime': 'Runtime',
  'agents.deleteTitle': 'Delete agent {name}?', 'agents.deleteText': 'Sessions that used it are kept.', 'agents.empty': 'No agents yet',
  'agents.offline': ' (offline)', 'agents.unavailable': ' (harness not enabled)', 'agents.unavailableHint': 'This harness is disabled or no longer detected; new sessions will fail. Pick another runtime.', 'agents.noRuntimes': 'No runtime available: enable a harness on the Daemons page first.',
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
