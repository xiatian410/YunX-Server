import {chromium} from '@playwright/test';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {startFixture} from './design-fixture.mjs';
const label=process.argv[2],css=process.argv[3]?await readFile(process.argv[3],'utf8'):'';
const out=resolve(import.meta.dirname,'../../.work/design',label);await mkdir(out,{recursive:true});const results=[];
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',args:['--no-sandbox']});
try{for(const theme of ['light','dark']){const fixture=await startFixture(theme);try{for(const width of [390,768,1440]){const page=await browser.newPage({viewport:{width,height:900},reducedMotion:'reduce'});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(fixture.url+'/#accounts');await page.locator('.heading-actions').getByRole('button',{name:'添加账号'}).click();await page.locator('mdui-select[label="所属平台"]').click();await page.locator('mdui-menu-item[value="xunlei"]').click();await page.locator('mdui-menu-item[value="xunlei"]').waitFor({state:'hidden'});if(css)await page.addStyleTag({content:css});await page.locator('mdui-text-field[label="设备 ID"]').waitFor();
 for(const view of ['credentials','quality']){
  if(view==='quality'){await page.getByRole('button',{name:'取消',exact:true}).click();await page.goto(fixture.url+'/#resolve');await page.route('**/api/resolve',r=>r.fulfill({json:{session_id:'fixture',platform:'bilibili',title:'摄影基础课程',items:[{id:'media-0',name:'摄影基础课程 - P1 相机设置.mp4',size:-1,directory:false}],parent:'0',page:1,more:false,qualities:[{id:80,name:'1080P 高清'},{id:32,name:'480P 清晰'}],quality:80,notice:''}}));await page.locator('mdui-text-field[label="分享链接或文件链接"] textarea').fill('https://www.bilibili.com/video/BV1xx411c7mD');await page.getByRole('button',{name:'解析链接',exact:true}).click();await page.locator('.quality-field').waitFor();if(css)await page.addStyleTag({content:css})}
  await page.evaluate(async()=>{await Promise.all([...document.querySelectorAll('*')].filter(e=>e.updateComplete).map(e=>e.updateComplete));await document.fonts.ready;await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))});
  const metrics=await page.evaluate(()=>({overflow:document.documentElement.scrollWidth>innerWidth,geometry:[...document.querySelectorAll('.quality-field,mdui-dialog[open] mdui-text-field,mdui-dialog[open] mdui-select')].map(e=>{const r=e.getBoundingClientRect();return{label:e.label,x:r.x,y:r.y,w:r.width,h:r.height}})}));
  await page.screenshot({path:resolve(out,`${view}-${width}-${theme}.png`),fullPage:true});results.push({view,width,theme,...metrics,errors:[...errors]});
 }await page.close()
}}finally{await fixture.close()}}}finally{await browser.close()}
await writeFile(resolve(out,'controls.json'),JSON.stringify(results,null,2));console.log(JSON.stringify({label,captures:results.length,overflow:results.filter(x=>x.overflow).length,errors:results.flatMap(x=>x.errors)}));
