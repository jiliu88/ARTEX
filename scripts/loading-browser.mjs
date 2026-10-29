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
await page.addInitScript(()=>localStorage.setItem('artex_token','synthetic-browser-only'));
let mode='stats-hang';const errors=[];
page.on('pageerror',e=>errors.push(e.message));
const fixture={id:'1',name:'合成加载回归任务',description:'合成加载回归任务',goal:'验证加载',status:'paused',paused:true,created_at:1,worker_count:1,goals_total:0,goals_met:0,source_tasks:[],llm_profile_ids:[]};
await page.route('**/api/**',async route=>{
 const url=new URL(route.request().url());
 if(url.pathname==='/api/stats' && mode==='stats-hang') {await new Promise(r=>setTimeout(r,17000));await route.abort().catch(()=>{});return;}
 if(url.pathname==='/api/tasks/1') {
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
 mode='task-hang';await page.reload();await page.getByRole('button',{name:'重试',exact:true}).waitFor({timeout:16500});
 console.log('PASS:挂起任务15秒后停止加载并允许重试');
 assert.deepEqual(errors,[]);
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
