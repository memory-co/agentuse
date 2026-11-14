import { useSyncExternalStore } from 'react';

// 中英文界面。中文里 device 叫「连接设备」,harness 叫「执行器」;agent、会话照旧。
// 选择记在 localStorage,没选过就跟浏览器语言。服务端返回的错误信息不翻译。
export type Locale = 'zh' | 'en';
export const locales: { value: Locale; label: string }[] = [{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }];

const zh = {
  'nest.parentTarget': '回传父会话',
  'nest.mode': '会话模式',
  'nest.single': '单体模式',
  'nest.collaborative': '协作模式',
  'nest.singleHint': 'Agent 在当前会话中直接完成任务。',
  'nest.collaborativeHint': 'Agent 将任务拆成子会话，协调执行并汇总结果。',
  'nest.children': '子会话',
  'nest.noChildren': '协调者尚未创建子会话。',
  'nest.parent': '返回父会话',
  'nest.source': '查看源消息与文件',
  'nest.deleteTree': '删除此会话及其全部子会话？',
  'nest.delivery.pending': '等待回传父会话',
  'nest.delivery.delivered': '已送达父会话',
  'nest.delivery.failed': '回传父会话失败',

  'common.loading': '加载中…', 'common.cancel': '取消', 'common.ok': '确定', 'common.delete': '删除', 'common.copy': '复制', 'common.copyFailed': '复制失败，请手动选中', 'common.close': '关闭',
  'common.name': '名字', 'common.description': '说明', 'common.user': '用户', 'common.admin': '管理员', 'common.normalUser': '普通用户', 'common.create': '创建', 'common.new': '新建',
  'nav.new': '新会话', 'nav.agents': 'Agent', 'nav.skills': 'Skills', 'nav.devices': '连接设备', 'nav.harnesses': '执行器', 'nav.users': '用户',
  'nav.conversations': '会话', 'nav.sessionsFailed': '会话加载失败', 'nav.noConversations': '让 agent 帮你干点活', 'nav.logout': '退出', 'nav.language': 'English',
  'nav.sheetTitle': '导航', 'nav.sheetText': '侧栏导航', 'nav.toggleSidebar': '展开或收起侧栏',
  'nav.main': '主导航', 'nav.openNav': '打开导航', 'nav.closeNav': '关闭导航',
  'actAs.label': '身份', 'actAs.self': '{name}(管理员，看全部)', 'actAs.acting': '代为 {id}', 'actAs.switch': '切换身份', 'actAs.search': '搜索用户…', 'actAs.empty': '没有匹配的用户',
  'login.title': '登录 agentuse', 'login.text': '输入登录令牌(ut_ 开头)。管理员令牌在服务第一次启动时打印在日志里；其他用户的令牌由管理员创建。', 'login.token': '登录令牌', 'login.invalidLink': '登录链接无效', 'login.submit': '登录',
  'conversation.status.idle': '已回复', 'conversation.status.pending': '排队中', 'conversation.status.running': '运行中', 'conversation.status.waiting': '等你回答', 'conversation.status.resolved': '已完成',
  'conversation.resolve': '标记完成', 'conversation.reopen': '取消完成', 'conversation.resolved': '已完成 ({n})',
  'nav.tabConversations': '会话',





  'mention.users': '用户收件人', 'mention.userId': '输入用户 ID', 'mention.addUser': '添加', 'mention.removeUser': '移除用户', 'mention.userHint': '用户必须已有会话读取权限。只选用户不会启动 Agent。', 'mention.recipients': '{agents} 个 Agent · {users} 位用户',
  'mention.multi': '执行 agent（可多选）', 'mention.multiHint': '勾选本条消息的执行者，最多 8 个；未选择 Agent 时不会启动执行。', 'mention.choose': '选择 agent', 'mention.search': '搜索名字或输入 agent id', 'mention.remove': '移除目标', 'mention.noMatch': '没有匹配项', 'mention.targets': '发送给 {n} 个 agent', 'mention.note': '未选择收件人，仅记入会话', 'mention.noteOnly': '仅记录 · 无收件人',
  'mention.steerMessages': '插话消息', 'mention.details': '执行详情', 'mention.detailsFor': '查看 @{agent} 的执行详情', 'mention.cancel': '取消执行', 'mention.shared': '共享对话：@ 选择本次执行者', 'mention.pending': '排队', 'mention.running': '执行中', 'mention.waiting': '等回答', 'mention.succeeded': '已完成', 'mention.failed': '失败', 'mention.cancelled': '已取消', 'mention.delivered': '已送达', 'mention.steer': '插话',
  'attachment.files': '文件',
  'attachment.search': '搜索文件',
  'attachment.inputs': '用户输入',
  'attachment.outputs': 'AI 输出',
  'attachment.preview': '文件预览',
  'attachment.noPreview': '此文件类型请下载后查看',
  'attachment.tooLarge': '文件较大，请下载后查看',
  'attachment.select': '选择文件查看内容',
  'attachment.attach': '添加到消息',
  'attachment.remove': '移除附件',
  'attachment.thisMessage': '本条消息的附件',
  'attachment.close': '关闭文件栏',
  'attachment.unreferenced': '尚未关联消息',
  'attachment.limit': '每条消息最多添加 64 个附件',
  'attachment.title': '附件', 'attachment.upload': '上传文件', 'attachment.download': '下载', 'attachment.delete': '删除附件？',
  'attachment.empty': '还没有附件', 'attachment.loading': '正在加载附件…', 'attachment.nextTurn': '新上传的文件下一轮可用；需要立即使用时请插话说明。',
  'attachment.selected': '已选 {n} 个文件', 'attachment.created': '附件上传失败时会保留空会话和已上传文件，可重试或进入会话处理。', 'attachment.open': '打开已创建的会话',
  'conversation.send': '发送', 'conversation.ask': '要 agent 做什么?', 'conversation.askText': '选择 Agent 发送消息；同一对话可以由不同设备上的多个 Agent 处理。',
  'conversation.taskPlaceholder': '描述任务，回车发送', 'conversation.agentPlaceholder': 'agent id,别人的也行',
  'conversation.noAgents': '还没有自己的 agent,先去左边「Agent」建一个，或者直接填别人的 agent id。',
  'conversation.metaHarness': '执行器 {harness}', 'conversation.metaOnBehalf': '{by} 发起，跑在 {owner} 的设备上', 'conversation.metaDevice': '连接设备 {id}', 'conversation.metaHarnessSession': '执行器会话 {id}',
  'conversation.deleteTitle': '删除这个会话?', 'conversation.deleteText': '只删 agentuse 里的记录。', 'conversation.delete': '删除会话',
  'conversation.replyPlaceholder': '接着说，回车发送', 'conversation.busyPlaceholder': '补充说明，会在它下一步之前送达', 'conversation.steerDelivered': '已送达', 'conversation.steerPending': '等待送达', 'conversation.deviceGone': '原来的连接设备已删除，这个会话不能继续。',
  'conversation.deviceOffline': '连接设备离线，上线后才会开始。', 'conversation.progress': '过程 {n} 条', 'conversation.failed': '失败',
  'question.other': '其他，自己写', 'question.submit': '回答', 'question.sending': '已回答，等待送达', 'question.answered': '已回答', 'question.expired': '没有回答，已过期', 'question.ask': '问', 'question.answer': '答',
  'agents.newTitle': '新建 agent', 'agents.newText': '修改配置会发布新版本；已入队的消息和运行中的任务保留原版本。',
  'agents.idPlaceholder': 'id,如 code-helper', 'agents.namePlaceholder': '名字，如 写代码助手', 'agents.pickRuntime': '选择运行时(设备 · 执行器)', 'agents.listTitle': 'Agent', 'agents.runtime': '运行时',
  'agents.deleteTitle': '删除 agent {name}?', 'agents.deleteText': '用过它的会话保留。', 'agents.empty': '还没有 agent',
  'agents.offline': '(离线)', 'agents.unavailable': '(执行器未开启)', 'agents.unavailableHint': '这个执行器已关闭或本机检测不到，新会话会直接报错，请换一个运行时。', 'agents.noRuntimes': '没有可用的运行时：先到「连接设备」页开启执行器。',
  'devices.status.pending': '待注册', 'devices.status.online': '在线', 'devices.status.offline': '离线',
  'devices.addTitle': '添加连接设备', 'devices.addText': '在要跑执行器的电脑上执行生成的安装命令，就能把它接进来。', 'devices.namePlaceholder': '名字(可选，默认用主机名)', 'devices.add': '添加连接设备',
  'devices.empty': '还没有连接设备，先添加一台。', 'devices.enabled': '已开启', 'devices.disabled': '已关闭', 'devices.metaUser': '用户 {id}', 'devices.metaSeen': '最近 {at}', 'devices.unnamed': '(未命名)',
  'devices.resetTitle': '重新生成安装命令?', 'devices.resetText': '这台电脑现有的密钥会立即失效，需要重新跑安装命令。', 'devices.reset': '重新生成安装命令',
  'devices.deleteTitle': '删除这台连接设备?', 'devices.deleteText': '它会断开，绑定在它上面的会话不能再继续。',
  'devices.runSetup': '在要接入的电脑上运行(令牌只能用一次):', 'devices.pickHarnesses': '选择这台电脑上开启哪些执行器:', 'devices.notDetected': ' · 本机未检测到',
  'devices.noHarnesses': '这台电脑还没上报可用的执行器(连上后会自动检测 claude、codex 等)。',
  'harnesses.newTitle': '注册自定义执行器', 'harnesses.newText1': '自己写的执行器：连接设备把 ', 'harnesses.newText2': ' 以 JSON 写进 stdin,程序往 stdout 按行输出 JSON(', 'harnesses.newText3': ',session_id 下次通过 resume 带回来)。注册后在连接设备的 ', 'harnesses.newText4': ' 里配置本机启动命令。',
  'harnesses.namePlaceholder': '名字，如 my-harness', 'harnesses.displayName': '显示名', 'harnesses.register': '注册', 'harnesses.listTitle': '全部执行器', 'harnesses.kind': '类型',
  'harnesses.builtin': '内置', 'harnesses.custom': '自定义', 'harnesses.deleteTitle': '删除执行器 {name}?',
  'users.newTitle': '新建用户', 'users.idPlaceholder': '用户 id(可选，填你们系统的 user id)', 'users.namePlaceholder': '用户名', 'users.created1': '已创建 ', 'users.created2': '。把下面的登录链接发给 TA(只显示这一次):',
  'users.listTitle': '用户', 'users.name': '用户名', 'users.role': '角色', 'users.createdAt': '创建',
};
type Key = keyof typeof zh;

const en: Record<Key, string> = {
  'nest.parentTarget': 'Return to parent',
  'nest.mode': 'Conversation mode',
  'nest.single': 'Single',
  'nest.collaborative': 'Collaborative',
  'nest.singleHint': 'Agents execute the request in this conversation.',
  'nest.collaborativeHint': 'Agents split the request into child conversations and coordinate the results.',
  'nest.children': 'Child conversations',
  'nest.noChildren': 'The coordinator has not created any child conversations yet.',
  'nest.parent': 'Parent conversation',
  'nest.source': 'View source message and files',
  'nest.deleteTree': 'Delete this conversation and all of its child conversations?',
  'nest.delivery.pending': 'Return to parent pending',
  'nest.delivery.delivered': 'Delivered to parent',
  'nest.delivery.failed': 'Return to parent failed',

  'common.loading': 'Loading…', 'common.cancel': 'Cancel', 'common.ok': 'OK', 'common.delete': 'Delete', 'common.copy': 'Copy', 'common.copyFailed': 'Copy failed, please select it manually', 'common.close': 'Close',
  'common.name': 'Name', 'common.description': 'Description', 'common.user': 'User', 'common.admin': 'Admin', 'common.normalUser': 'User', 'common.create': 'Create', 'common.new': 'Create',
  'nav.new': 'New conversation', 'nav.agents': 'Agents', 'nav.skills': 'Skills', 'nav.devices': 'Devices', 'nav.harnesses': 'Harnesses', 'nav.users': 'Users',
  'nav.conversations': 'Conversations', 'nav.sessionsFailed': 'Failed to load conversations', 'nav.noConversations': 'Ask an agent to do something', 'nav.logout': 'Log out', 'nav.language': '中文',
  'nav.sheetTitle': 'Navigation', 'nav.sheetText': 'Sidebar navigation', 'nav.toggleSidebar': 'Toggle sidebar',
  'nav.main': 'Main navigation', 'nav.openNav': 'Open navigation', 'nav.closeNav': 'Close navigation',
  'actAs.label': 'Acting as', 'actAs.self': '{name} (admin, see everything)', 'actAs.acting': 'Acting as {id}', 'actAs.switch': 'Switch identity', 'actAs.search': 'Search users…', 'actAs.empty': 'No matching users',
  'login.title': 'Sign in to agentuse', 'login.text': 'Enter your login token (starts with ut_). The admin token is printed in the log when the server first starts; other users get theirs from the admin.', 'login.token': 'Login token', 'login.invalidLink': 'Invalid login link', 'login.submit': 'Sign in',
  'conversation.status.idle': 'Replied', 'conversation.status.pending': 'Queued', 'conversation.status.running': 'Running', 'conversation.status.waiting': 'Needs your answer', 'conversation.status.resolved': 'Done',
  'conversation.resolve': 'Mark done', 'conversation.reopen': 'Reopen', 'conversation.resolved': 'Done ({n})',
  'nav.tabConversations': 'Conversations',





  'mention.users': 'User recipients', 'mention.userId': 'Enter user ID', 'mention.addUser': 'Add', 'mention.removeUser': 'Remove user', 'mention.userHint': 'Users must already have access to this conversation. User recipients do not start agents.', 'mention.recipients': '{agents} agents · {users} users',
  'mention.multi': 'Agents (select multiple)', 'mention.multiHint': 'Choose up to 8 agents for this message. Without agents, no execution starts.', 'mention.choose': 'Choose agents', 'mention.search': 'Search name or agent id', 'mention.remove': 'Remove target', 'mention.noMatch': 'No matches', 'mention.targets': 'Send to {n} agents', 'mention.note': 'No recipients; save as a note', 'mention.noteOnly': 'Note · No recipients',
  'mention.steerMessages': 'Follow-up messages', 'mention.details': 'Run details', 'mention.detailsFor': 'View run details for @{agent}', 'mention.cancel': 'Cancel run', 'mention.shared': 'Shared conversation: @ agents for this message', 'mention.pending': 'Queued', 'mention.running': 'Running', 'mention.waiting': 'Waiting for answer', 'mention.succeeded': 'Completed', 'mention.failed': 'Failed', 'mention.cancelled': 'Cancelled', 'mention.delivered': 'Delivered', 'mention.steer': 'Follow-up',
  'attachment.files': 'Files',
  'attachment.search': 'Search files',
  'attachment.inputs': 'User input',
  'attachment.outputs': 'AI output',
  'attachment.preview': 'File preview',
  'attachment.noPreview': 'Download this file to view it',
  'attachment.tooLarge': 'Download this large file to view it',
  'attachment.select': 'Select a file to preview',
  'attachment.attach': 'Attach to message',
  'attachment.remove': 'Remove attachment',
  'attachment.thisMessage': 'Attachments for this message',
  'attachment.close': 'Close files',
  'attachment.unreferenced': 'Not attached to a message',
  'attachment.limit': 'A message can include up to 64 attachments',
  'attachment.title': 'Attachments', 'attachment.upload': 'Upload files', 'attachment.download': 'Download', 'attachment.delete': 'Delete attachment?',
  'attachment.empty': 'No attachments yet', 'attachment.loading': 'Loading attachments…', 'attachment.nextTurn': 'New uploads are available next turn. Send a follow-up if needed now.',
  'attachment.selected': '{n} files selected', 'attachment.created': 'If upload fails, the empty conversation and uploaded files are kept. Retry or open the conversation.', 'attachment.open': 'Open created conversation',
  'conversation.send': 'Send', 'conversation.ask': 'What should the agent do?', 'conversation.askText': "The conversation runs on the agent's current runtime (a harness on one of its owner's devices), and keeps resuming on that same machine.",
  'conversation.taskPlaceholder': 'Describe the task, Enter to send', 'conversation.agentPlaceholder': "agent id, anyone's works",
  'conversation.noAgents': "You have no agents yet. Create one under “Agents” on the left, or enter someone else's agent id.",
  'conversation.metaHarness': 'harness {harness}', 'conversation.metaOnBehalf': "started by {by}, runs on {owner}'s machine", 'conversation.metaDevice': 'device {id}', 'conversation.metaHarnessSession': 'harness session {id}',
  'conversation.deleteTitle': 'Delete this conversation?', 'conversation.deleteText': 'Only the record in agentuse is deleted.', 'conversation.delete': 'Delete conversation',
  'conversation.replyPlaceholder': 'Reply, Enter to send', 'conversation.busyPlaceholder': 'Add a note, delivered before its next step', 'conversation.steerDelivered': 'Delivered', 'conversation.steerPending': 'Waiting to deliver', 'conversation.deviceGone': 'The original device was deleted, so this conversation cannot continue.',
  'conversation.deviceOffline': 'The device is offline; this starts once it comes back online.', 'conversation.progress': '{n} progress updates', 'conversation.failed': 'Failed',
  'question.other': 'Other, write your own', 'question.submit': 'Answer', 'question.sending': 'Answered, waiting to deliver', 'question.answered': 'Answered', 'question.expired': 'Not answered, expired', 'question.ask': 'Q', 'question.answer': 'A',
  'agents.newTitle': 'New agent', 'agents.newText': 'Configuration changes publish a new version. Queued and running tasks retain their pinned version.',
  'agents.idPlaceholder': 'id, e.g. code-helper', 'agents.namePlaceholder': 'Name, e.g. Code helper', 'agents.pickRuntime': 'Pick a runtime (device · harness)', 'agents.listTitle': 'Agents', 'agents.runtime': 'Runtime',
  'agents.deleteTitle': 'Delete agent {name}?', 'agents.deleteText': 'Conversations that used it are kept.', 'agents.empty': 'No agents yet',
  'agents.offline': ' (offline)', 'agents.unavailable': ' (harness not enabled)', 'agents.unavailableHint': 'This harness is disabled or no longer detected; new conversations will fail. Pick another runtime.', 'agents.noRuntimes': 'No runtime available: enable a harness on the Devices page first.',
  'devices.status.pending': 'Not registered', 'devices.status.online': 'Online', 'devices.status.offline': 'Offline',
  'devices.addTitle': 'Add a device', 'devices.addText': 'Run the generated install command on the computer that should run harnesses to register it as a device.', 'devices.namePlaceholder': 'Name (optional, defaults to hostname)', 'devices.add': 'Add device',
  'devices.empty': 'No devices yet. Add one first.', 'devices.enabled': 'Enabled', 'devices.disabled': 'Disabled', 'devices.metaUser': 'user {id}', 'devices.metaSeen': 'last seen {at}', 'devices.unnamed': '(unnamed)',
  'devices.resetTitle': 'Regenerate the install command?', 'devices.resetText': "This computer's current key stops working immediately and the install command must be run again.", 'devices.reset': 'Regenerate install command',
  'devices.deleteTitle': 'Delete this device?', 'devices.deleteText': 'It disconnects, and conversations bound to it cannot continue.',
  'devices.runSetup': 'Run this on the computer to connect (the token works once):', 'devices.pickHarnesses': 'Choose which harnesses to enable on this computer:', 'devices.notDetected': ' · not detected on this machine',
  'devices.noHarnesses': 'This computer has not reported any harnesses yet (claude, codex and others are detected once it connects).',
  'harnesses.newTitle': 'Register a custom harness', 'harnesses.newText1': 'Your own harness: the device writes ', 'harnesses.newText2': ' as JSON to stdin, and the program prints JSON lines to stdout (', 'harnesses.newText3': '; session_id comes back via resume next time). After registering, set the local launch command in the device\'s ', 'harnesses.newText4': '.',
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
