export const MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;

export function validatePolicy(label, senders) {
  if (typeof label !== 'string' || !label.trim() || label.length > 100 || /[\r\n]/.test(label)) throw Error('请指定独立的测试邮件标签');
  if (!Array.isArray(senders) || !senders.length || senders.length > 20) throw Error('请指定允许的测试邮件发件人');
  const normalized = [...new Set(senders.map(s => String(s).trim().toLowerCase()))];
  if (normalized.some(s => !/^[^\s<>@,;]+@[^\s<>@,;]+\.[^\s<>@,;]+$/.test(s))) throw Error('允许的发件人必须是完整邮箱地址');
  return {label: label.trim(), senders: normalized};
}

export function header(message, name) {
  return (message.payload?.headers || []).find(h => h.name.toLowerCase() === name.toLowerCase())?.value || '';
}

export function assertAllowed(message, labelId, policy, now = Date.now()) {
  if (!message.labelIds?.includes(labelId)) throw Error('邮件不属于已授权的测试标签');
  const from = header(message, 'From');
  const match = from.match(/<([^<>]+)>/);
  const address = (match ? match[1] : from).trim().toLowerCase();
  if (!policy.senders.includes(address)) throw Error('邮件发件人不在允许列表中');
  const timestamp = Number(message.internalDate);
  if (!Number.isFinite(timestamp) || timestamp > now + 60000 || timestamp < now - MAX_AGE_MS) throw Error('邮件不在最近 7 天的测试范围内');
  return message;
}

export function mailText(payload) {
  const parts = [];
  function visit(p) {
    if (p.filename || p.body?.attachmentId) return;
    if (p.body?.data && ['text/plain', 'text/html'].includes(p.mimeType)) parts.push({type: p.mimeType, text: Buffer.from(p.body.data, 'base64url').toString('utf8')});
    for (const child of p.parts || []) visit(child);
  }
  visit(payload || {});
  const plain = parts.filter(p => p.type === 'text/plain');
  if (plain.length) return plain.map(p => p.text).join('\n').slice(0, 16000);
  return parts.map(p => p.text.replace(/<(script|style)\b[^>]*>[\s\S]*?<\/\1>/gi, '').replace(/<a\b[^>]*href=["'](https?:\/\/[^"']+)["'][^>]*>([\s\S]*?)<\/a>/gi, '$2 ($1)').replace(/<[^>]*>/g, ' ').replace(/&nbsp;/g, ' ').replace(/&amp;/g, '&')).join('\n').slice(0, 16000);
}
