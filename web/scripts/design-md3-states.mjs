import {chromium} from '@playwright/test';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {startFixture} from './design-fixture.mjs';
const label=process.argv[2]||'md3-draft',css=process.argv[3]?await readFile(process.argv[3],'utf8'):'';
const out=resolve(import.meta.dirname,'../../.work/design',label);await mkdir(out,{recursive:true});
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',args:['--no-sandbox']});const results=[];
async function capture(page,view,width,theme){
 if(css)await page.addStyleTag({content:css});await page.mouse.move(0,0);
 await page.evaluate(async()=>{
  await document.fonts.ready;
  const elements=[...document.querySelectorAll('*')];
  for(let i=0;i<elements.length;i++)if(elements[i].shadowRoot)elements.push(...elements[i].shadowRoot.querySelectorAll('*'));
  await Promise.all(elements.filter(e=>e.updateComplete).map(e=>e.updateComplete));
  // Finish only finite visual feedback for deterministic still captures; no product delays.
  for(const e of elements)for(const a of e.getAnimations())if(a.effect?.getComputedTiming().iterations!==Infinity)try{a.finish()}catch{}
  await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
 });
 const metrics=await page.evaluate(()=>{
  const selectors=['.main-content','.page-heading','.resolve-form','.task-row','.account-row','.setting-row','.mobile-nav','.sidebar','mdui-dialog[open]'];
  const geometry=Object.fromEntries(selectors.map(s=>[s,[...document.querySelectorAll(s)].map(e=>{const r=e.getBoundingClientRect(),v=getComputedStyle(e);return{x:r.x,y:r.y,w:r.width,h:r.height,display:v.display,order:v.order,grid:v.gridTemplateColumns}})]));
  const helpers=[...document.querySelectorAll('mdui-dialog[open] mdui-text-field[helper]')].map(e=>{const host=e.getBoundingClientRect(),support=e.shadowRoot?.querySelector('[part="supporting"]')?.getBoundingClientRect();return{label:e.label,spill:support?Math.max(0,support.bottom-host.bottom):0}});
  function tree(e){return e?[e.tagName,e.getAttribute('class')||'',[...e.children].map(tree)]:null}
  return{geometry,helpers,structure:tree(document.querySelector('.app-shell')||document.querySelector('.login-page')),overflow:document.documentElement.scrollWidth>innerWidth,images:[...document.images].every(i=>i.complete&&i.naturalWidth>0),primary:getComputedStyle(document.documentElement).getPropertyValue('--mdui-color-primary').trim(),font:getComputedStyle(document.body).fontFamily};
 });
 await page.screenshot({path:resolve(out,`${view}-${width}-${theme}.png`),fullPage:false});return metrics;
}
try{for(const theme of ['light','dark']){const f=await startFixture(theme);try{for(const width of [390,768,1440]){
 const page=await browser.newPage({viewport:{width,height:900},colorScheme:theme,reducedMotion:'reduce'}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 for(const view of ['resolve-empty','credentials','setting-limit','setting-theme','quality','login']){
  await page.unrouteAll();await page.goto(f.url+'/?capture='+view+'#'+(view==='credentials'?'accounts':view.startsWith('setting')?'settings':'resolve'));await page.locator('.main-content h1').waitFor();
  if(view==='credentials'){await page.locator('.heading-actions').getByRole('button',{name:'添加账号'}).click();await page.locator('mdui-select[label="所属平台"]').click();await page.locator('mdui-menu-item[value="xunlei"]').click();await page.locator('mdui-text-field[label="设备 ID"]').waitFor();await page.locator('mdui-menu-item[value="xunlei"]').waitFor({state:'hidden'})}
  if(view.startsWith('setting')){await page.locator(`[data-setting="${view==='setting-limit'?'limit':'theme'}"]`).click();await page.locator('mdui-dialog[open]').waitFor()}
  if(view==='quality'){
   await page.route('**/api/resolve',r=>r.fulfill({json:{session_id:'fixture',platform:'bilibili',title:'摄影基础课程',items:[{id:'media-0',name:'摄影基础课程 - P1 相机设置.mp4',size:-1,directory:false}],parent:'0',page:1,more:false,qualities:[{id:80,name:'1080P 高清'},{id:32,name:'480P 清晰'}],quality:80,notice:''}}));
   await page.locator('mdui-text-field[label="分享链接或文件链接"] textarea').fill('https://www.bilibili.com/video/BV1xx411c7mD');await page.getByRole('button',{name:'开始解析',exact:true}).click();await page.locator('.quality-field').waitFor();
  }
  if(view==='login'){await page.route('**/api/auth/session',r=>r.fulfill({json:{authenticated:false,name:'云析'}}));await page.reload();await page.locator('.login-panel').waitFor()}
  results.push({view,width,theme,...await capture(page,view,width,theme),errors:[...errors]});
 }
 await page.close();
}}finally{await f.close()}}}finally{await browser.close()}
await writeFile(resolve(out,'states.json'),JSON.stringify(results,null,2));console.log(JSON.stringify({label,captures:results.length,overflow:results.filter(x=>x.overflow).length,helperSpill:results.flatMap(x=>x.helpers).filter(x=>x.spill>1),errors:results.flatMap(x=>x.errors)}));
