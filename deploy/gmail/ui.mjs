const $ = id => document.getElementById(id);
async function api(path, data) {
  const response = await fetch(`api/${path}`, {method: data === undefined ? 'GET' : 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/json'}, ...(data === undefined ? {} : {body: JSON.stringify(data)})});
  const value = await response.json(); if (!response.ok) throw Error(value.error || '请求未成功'); return value;
}
async function load() {
  const s = await api('status');
  $('status').textContent = s.connected ? `Google 已连接；${s.policy_ready ? '测试邮件范围已配置' : '请配置测试标签和允许的发件人'}` : `等待 Google 授权；${s.client_configured ? '客户端已配置' : '请先配置 OAuth 桌面客户端'}`;
  $('authorize').disabled = !s.client_configured || s.connected;
  $('label').value = s.label; $('senders').value = s.senders.join('\n');
}
for (const id of ['upload', 'policy', 'authorize']) $(id).onclick = async () => {
  const button = $(id); button.disabled = true; $('message').textContent = '';
  try {
    if (id === 'upload') {const file = $('client').files[0]; if (!file) throw Error('请选择 Google 官方客户端配置文件'); await api('client', JSON.parse(await file.text())); $('client').value = ''; $('message').textContent = '客户端已保存。';}
    if (id === 'policy') {await api('policy', {label: $('label').value, senders: $('senders').value.split(/[\n,]/).map(s => s.trim()).filter(Boolean)}); $('message').textContent = '读取范围已保存。';}
    if (id === 'authorize') {const result = await api('authorize', {}); window.location.assign(result.url); return;}
    await load();
  } catch (e) {$('message').textContent = e.message;} finally {button.disabled = false;}
};
load().catch(e => {$('status').textContent = e.message;});
