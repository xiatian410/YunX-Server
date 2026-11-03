import {chromium}from'@playwright/test';
import{mkdir,writeFile,readFile}from'node:fs/promises';
import{resolve}from'node:path';
import{startFixture}from'./design-fixture.mjs';
const label=process.argv[2]||'baseline';const override=process.argv[3]?await readFile(process.argv[3],'utf8'):'';
const out=resolve(import.meta.dirname,'../../.work/design',label);await mkdir(out,{recursive:true});
let taste;try{taste=await import('/root/.codex/skills/design-taste-frontend/scripts/lib/probe.mjs')}catch{}
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',args:['--no-sandbox']});const results=[];
try{for(const theme of ['light','dark']){const srv=await startFixture(theme);try{for(const width of [390,768,1440]){const page=await browser.newPage({viewport:{width,height:900},reducedMotion:'reduce'});const errors=[];page.on('pageerror',e=>errors.push(e.message));for(const route of ['downloads','resolve','accounts','settings']){
 await page.goto(srv.url+'/#'+route);await page.locator('.main-content h1').waitFor();await page.waitForFunction(()=>document.querySelector('.connection')?.textContent?.includes('已连接'));await page.evaluate(()=>document.fonts.ready);if(override)await page.addStyleTag({content:override});
 if(route==='resolve'){await page.locator('mdui-text-field[label="分享链接或文件链接"] textarea').fill('https://pan.quark.cn/s/design-fixture');await page.getByRole('button',{name:'开始解析',exact:true}).click();await page.locator('.browser-row').first().waitFor()}
 // Wait for component updates, not a timer or network polling.
 await page.mouse.move(0,0);
 await page.evaluate(async()=>{const all=[...document.querySelectorAll('*')];for(let i=0;i<all.length;i++)if(all[i].shadowRoot)all.push(...all[i].shadowRoot.querySelectorAll('*'));await Promise.all(all.filter(x=>x.updateComplete).map(x=>x.updateComplete));for(const e of all)for(const a of e.getAnimations())if(a.effect?.getComputedTiming().iterations!==Infinity)try{a.finish()}catch{}});
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 const result=await page.evaluate(()=>{
  const selectors=['.sidebar','.main-content','.page-heading','.summary-strip','.task-panel','.activity','.resolve-form','.file-browser','.account-list','.settings-section','.password-section','.mobile-nav','.task-row','.progress-label','.file-title','.file-meta','.account-info h2','.browser-heading h2','.filters button'];
  const geometry={};for(const sel of selectors){geometry[sel]=[...document.querySelectorAll(sel)].map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return{x:r.x,y:r.y,w:r.width,h:r.height,font:s.fontSize,weight:s.fontWeight,color:s.color,background:s.backgroundColor,gap:s.gap,align:s.alignItems,order:s.order,grid:s.gridTemplateColumns}})}
  const th=document.querySelector('.table-head>span');const title=document.querySelector('.file-title');let headerDelta=null;if(th&&getComputedStyle(th.parentElement).display!=='none'){const range=document.createRange();range.selectNodeContents(th);headerDelta=Math.abs(range.getBoundingClientRect().x-title.getBoundingClientRect().x)}
 const helpers=[...document.querySelectorAll('.settings-fields > mdui-text-field[helper]')].map(e=>{const r=e.getBoundingClientRect();const h=e.shadowRoot.querySelector('[part="supporting"]').getBoundingClientRect();return{label:e.label,inside:h.bottom<=r.bottom+1,spill:h.bottom-r.bottom}});
 function tree(e){return[e.tagName,e.getAttribute('class')||'',[...e.children].map(tree)]}
  return{headerPadding:th?getComputedStyle(th).paddingLeft:null,headerDelta,helpers,geometry,structure:tree(document.querySelector('.app-shell')),overflow:document.documentElement.scrollWidth>innerWidth,images:[...document.images].every(x=>x.complete&&x.naturalWidth>0)};
 });
 result.namedFixes=await page.evaluate(()=>{
 const account=document.querySelector('.account-info h2'),platform=document.querySelector('.platform-info h3'),github=document.querySelector('img[src="/icons/github.png"]');
 return{accountTextDelta:account&&platform?Math.abs(account.getBoundingClientRect().x-platform.getBoundingClientRect().x):null,githubBackground:github?getComputedStyle(github).backgroundColor:null};
 });
 if(width>=600){const active=page.locator('.sidebar nav a.active');await active.hover();result.namedFixes.activeHoverBackground=await active.locator('.nav-icon').evaluate(async e=>{getComputedStyle(e).backgroundColor;for(const a of e.getAnimations())a.finish();await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));return getComputedStyle(e).backgroundColor});await page.mouse.move(0,0);await active.locator('.nav-icon').evaluate(e=>{getComputedStyle(e).backgroundColor;for(const a of e.getAnimations())a.finish()})}
 if(taste){result.taste=await page.evaluate(taste.probe);result.appMetrics=await page.evaluate(taste.app)}
 const file=`${route}-${width}-${theme}.png`;await page.screenshot({path:resolve(out,file),fullPage:false});results.push({route,width,theme,file,...result,errors:[...errors]});
}await page.close()}}finally{await srv.close()}}}finally{await browser.close()}
await writeFile(resolve(out,'measurements.json'),JSON.stringify(results,null,2));console.log(JSON.stringify({label,captures:results.length,overflow:results.filter(x=>x.overflow).length,errors:results.flatMap(x=>x.errors),images:results.every(x=>x.images)}));
