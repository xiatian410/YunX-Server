// Private process worker. Browser traffic uses Go's authenticated, IP-checked proxy.
import {chromium} from 'playwright-core';
import {readFileSync,existsSync,mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
const input=readFileSync(0,'utf8');if(input.length>1024*1024)process.exit(2);
const request=JSON.parse(input);const timeout=Math.min(40000,request.timeout||30000);
const deadline=setTimeout(()=>process.exit(3),timeout+3000);deadline.unref();
const browserFiles=mkdtempSync(join(tmpdir(),'yunx-browser-'));
let browser;
try{
 const executablePath=process.env.YUNX_CHROMIUM||(['/usr/bin/chromium','/usr/bin/google-chrome'].find(existsSync));
 if(!process.env.YUNX_BROWSER_PROXY||!process.env.YUNX_BROWSER_PROXY_TOKEN)throw Error('Browser proxy missing');
 browser=await chromium.launch({executablePath,headless:true,env:{...process.env,XDG_CONFIG_HOME:browserFiles,XDG_CACHE_HOME:browserFiles},args:['--no-sandbox','--disable-dev-shm-usage','--proxy-bypass-list=<-loopback>'],proxy:{server:process.env.YUNX_BROWSER_PROXY,username:'yunx',password:process.env.YUNX_BROWSER_PROXY_TOKEN}});
 const context=await browser.newContext({userAgent:request.userAgent,serviceWorkers:'block',acceptDownloads:false});
 const cookies=(request.cookie||'').split(';').map(x=>{const i=x.indexOf('=');return i<1?null:{name:x.slice(0,i).trim(),value:x.slice(i+1).trim(),url:new URL(request.url).origin}}).filter(Boolean);
 if(cookies.length)await context.addCookies(cookies);
 let count=0;await context.route('**/*',route=>{const u=new URL(route.request().url());return (++count>250||!['http:','https:'].includes(u.protocol))?route.abort():route.continue()});
 await context.routeWebSocket('**/*',socket=>socket.close());
 const page=await context.newPage();page.setDefaultTimeout(timeout);page.setDefaultNavigationTimeout(timeout);
 for(const js of request.init||[])await context.addInitScript({content:js});
 const start=Date.now();await page.goto(request.url,{waitUntil:'domcontentloaded'});
 await page.waitForFunction(code=>{const v=(0,eval)(code);return typeof v==='function'?v():v},request.readyCheckJs,{timeout:Math.max(1,timeout-(Date.now()-start)),polling:'raf'});
 const result=await page.evaluate(code=>{const v=(0,eval)(code);return typeof v==='function'?v():v},request.extractJs);const text=typeof result==='string'?result:JSON.stringify(result);
 if(Buffer.byteLength(text)>2*1024*1024)throw Error('Browser response too large');process.stdout.write(text);
}catch{process.exitCode=1}finally{clearTimeout(deadline);if(browser)await browser.close();rmSync(browserFiles,{recursive:true,force:true})}
