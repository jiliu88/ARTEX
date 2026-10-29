import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
const { chromium }=await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const root=path.resolve('web/out');
const server=createServer(async(req,res)=>{
  const pathname=decodeURIComponent(new URL(req.url,'http://local').pathname);
  for(const candidate of [pathname,pathname+'/index.html',pathname+'.html']) {
    const file=path.resolve(root,'.'+candidate);
    if(!file.startsWith(root+'/'))continue;
    try{if(!(await stat(file)).isFile())continue;
      const ext=path.extname(file);res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json','.svg':'image/svg+xml'})[ext]||'application/octet-stream');
      res.end(await readFile(file));return;
    }catch{}
  }res.writeHead(404);res.end();
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=await chromium.launch({headless:true});
const page=await browser.newPage();
await page.addInitScript(()=>{
 if(!/^\/login\/?$/.test(location.pathname))localStorage.setItem('artex_token','synthetic-browser-only');
});
let mode='stats-hang';const errors=[];
page.on('pageerror',e=>errors.push(e.message));
const fixture={id:'1',name:'合成加载回归任务',description:'合成加载回归任务',goal:'验证加载',status:'paused',paused:true,created_at:1,worker_count:1,goals_total:0,goals_met:0,source_tasks:[],llm_profile_ids:[]};
await page.route('**/api/**',async route=>{
 const url=new URL(route.request().url());
 if(url.pathname==='/api/traffic') {
  if(mode==='traffic-hang'){await new Promise(r=>setTimeout(r,17000));await route.abort().catch(()=>{});return;}
  await route.fulfill({json:{enabled:true,exchanges:[],total:0,page:0,size:100}});return;
 }
 if(url.pathname==='/api/stats' && mode==='stats-hang') {await new Promise(r=>setTimeout(r,17000));await route.abort().catch(()=>{});return;}
 if(url.pathname==='/api/tasks/2') {await route.fulfill({json:{...fixture,id:'2',name:'第二个合成任务',description:'第二个合成任务'}});return;}
 if(url.pathname==='/api/tasks/1') {
  if(mode==='offline'){await route.abort('internetdisconnected');return;}
  if(mode==='401'){await route.fulfill({status:401,json:{error:'未授权'}});return;}
  if(mode==='task-hang'){await new Promise(r=>setTimeout(r,17000));await route.abort().catch(()=>{});return;}
  if(mode==='404'||mode==='500'){await route.fulfill({status:Number(mode),json:{error:mode==='404'?'task not found':'合成服务器故障'}});return;}
  await route.fulfill({json:fixture});return;
 }
 const body=url.pathname==='/api/auth/status'?{initialized:true}:url.pathname==='/api/intercept/pending/count'?{count:0}:url.pathname==='/api/tasks/1/chat/status'?{running:false}:url.pathname==='/api/stats'?{engine_mode:'paused',exploration:{},active_task:fixture}:url.pathname.includes('llm/profiles')?[]:url.pathname.endsWith('/coverage')?{enabled:false,by_type:[]}:url.pathname.endsWith('/goals')?{goals:[]}:url.pathname.endsWith('/constraints')?{constraints:[]}:url.pathname.endsWith('/scope')?{scope:[]}:url.pathname.endsWith('/activity/history')?{items:[],has_more:false}:url.pathname.includes('/activity/stream')?'':url.pathname.includes('/intents')?{items:[],has_more:false}:url.pathname.endsWith('/tokens')?{workers:[],sessions:[],total:{}}:{items:[],pending:[],assets:[],models:[],scope:[],goals:[],constraints:[],count:0,total:0};
 await route.fulfill({json:body});
});
const url=`http://127.0.0.1:${server.address().port}/function/tasks/detail/?id=1&tab=assets`;
try {
 await page.goto(url);await page.getByText('合成加载回归任务',{exact:true}).first().waitFor({timeout:5000});
 console.log('PASS:统计挂起不阻止首屏');
 mode='404';await page.reload();await page.getByText('任务 1 已被删除、归档或不存在',{exact:true}).waitFor();
 mode='500';await page.reload();await page.getByRole('button',{name:'重试',exact:true}).waitFor();
 mode='ok';await page.getByRole('button',{name:'重试',exact:true}).click();await page.getByText('合成加载回归任务',{exact:true}).first().waitFor();
 console.log('PASS:404/500区分及重试恢复');
 mode='offline';await page.getByText('刷新失败：',{exact:false}).waitFor({timeout:8000});
 assert.ok(await page.getByText('合成加载回归任务',{exact:true}).first().isVisible());
 mode='ok';await page.getByRole('button',{name:'重试',exact:true}).click();
 await page.getByText('刷新失败：',{exact:false}).waitFor({state:'hidden',timeout:5000});
 console.log('PASS:断网保留内容并可恢复');
 mode='task-hang';await page.reload();await page.getByRole('button',{name:'重试',exact:true}).waitFor({timeout:16500});
 console.log('PASS:挂起任务15秒后停止加载并允许重试');
 await page.evaluate(()=>history.pushState({},'', '/function/tasks/detail/?id=2&tab=assets'));
 await page.getByText('第二个合成任务',{exact:true}).first().waitFor({timeout:5000});
 console.log('PASS:任务切换不显示旧请求结果');
 const trafficActive=new Set();let trafficMax=0,trafficStarts=0;
 page.on('request',request=>{if(new URL(request.url()).pathname==='/api/traffic'){trafficActive.add(request);trafficStarts++;trafficMax=Math.max(trafficMax,trafficActive.size);}});
 const trafficEnded=request=>trafficActive.delete(request);
 page.on('requestfinished',trafficEnded);page.on('requestfailed',trafficEnded);
 mode='traffic-hang';
 const trafficFailed=page.waitForEvent('requestfailed',{predicate:request=>new URL(request.url()).pathname==='/api/traffic',timeout:17000});
 await page.goto(url.replace('tab=assets','tab=traffic'));await trafficFailed;
 assert.equal(trafficStarts,1);assert.equal(trafficMax,1);
 mode='ok';await page.waitForResponse(response=>new URL(response.url()).pathname==='/api/traffic'&&response.status()===200,{timeout:7000});
 assert.equal(trafficMax,1);
 console.log('PASS:新增任务流量挂起时并发不超过1且超时后恢复');
 mode='401';await page.goto(url);await page.waitForURL(/\/login\/?$/,{timeout:5000});
 assert.equal(await page.evaluate(()=>localStorage.getItem('artex_token')),null);
 console.log('PASS:401跳转登录并清除失效会话');
 assert.deepEqual(errors,[]);
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
