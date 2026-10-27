import {test} from 'node:test';
import assert from 'node:assert/strict';
import {assertAllowed, validatePolicy, mailText, MAX_AGE_MS} from './policy.mjs';

const now = 1800000000000;
const policy = validatePolicy('ARTEX-Test', ['noreply@example.com']);
const good = {id: 'synthetic-test', labelIds: ['Label_Test'], internalDate: String(now), payload: {headers: [{name: 'From', value: 'Example <noreply@example.com>'}]}};
test('标签约束不能被邮件 ID 或查询条件绕过', () => {
  assert.equal(assertAllowed(good, 'Label_Test', policy, now), good);
  assert.throws(() => assertAllowed({...good, labelIds: ['INBOX']}, 'Label_Test', policy, now));
});
test('显示名称、相近域名和子域名不能冒充允许地址', () => {
  for (const value of ['noreply@example.com <attacker@example.net>', 'noreply@example.com.evil.test', 'noreply@sub.example.com']) {
    assert.throws(() => assertAllowed({...good, payload: {headers: [{name: 'From', value}]}}, 'Label_Test', policy, now));
  }
});
test('过期、未来和无效日期邮件被拒绝', () => {
  for (const date of [now - MAX_AGE_MS - 1, now + 120000, 'invalid']) assert.throws(() => assertAllowed({...good, internalDate: String(date)}, 'Label_Test', policy, now));
});
test('空策略、通配符和整个域名授权被拒绝', () => {
  for (const senders of [[], ['*'], ['@example.com'], ['noreply@example.com\nother@example.net']]) assert.throws(() => validatePolicy('ARTEX-Test', senders));
});
test('附件不读取且优先使用纯文本', () => {
  const data = t => Buffer.from(t).toString('base64url');
  assert.equal(mailText({parts: [{mimeType: 'text/plain', body: {data: data('测试邮件')}}, {mimeType: 'text/html', body: {data: data('<b>HTML</b>')}}, {filename: 'private.txt', mimeType: 'text/plain', body: {data: data('不应读取')}}]}), '测试邮件');
});
