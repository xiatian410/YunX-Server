// Browser-only fixture server. Never imported by production code or embedded assets.
import {createServer} from 'node:http';
import{readFile}from'node:fs/promises';
import{resolve,extname}from'node:path';
export const fixture={version:'0.1.0',updated_at:'2026-11-03T12:00:00Z',root:'/downloads',settings:{name:'云析',theme:'light',concurrency:2,connections:4,speed_limit:0,default_dir:'媒体',resume_on_restart:true},disk:{available:648623947776,total:1099511627776},platforms:[{id:'quark',name:'夸克网盘',credential:'cookie',cloud:true},{id:'uc',name:'UC 网盘',credential:'cookie',cloud:true},{id:'pan123',name:'123 云盘',credential:'token',cloud:true},{id:'bilibili',name:'哔哩哔哩',credential:'cookie',cloud:false}],accounts:[{id:'a1',name:'我的夸克网盘',platform:'quark',status:'valid',nickname:'家庭资料',message:'账号检查通过'},{id:'a2',name:'123 媒体账号',platform:'pan123',status:'expired',message:'平台未确认登录状态，请更新凭据'}],events:[{id:1,task_id:'t2',kind:'completed',message:'文件已保存到下载目录',created_at:'2026-11-03T11:42:00Z'}],tasks:[{id:'t1',name:'山野之间 - 第 01 集.mp4',platform:'quark',status:'downloading',size:2147483648,downloaded:1014686023,speed:11010048,directory:'媒体/纪录片',created_at:'2026-11-03T11:40:00Z',updated_at:'2026-11-03T12:00:00Z',has_file:false},{id:'t2',name:'家庭照片备份 2026.zip',platform:'direct',status:'completed',size:841472000,downloaded:841472000,speed:0,directory:'备份',created_at:'2026-11-03T11:39:00Z',updated_at:'2026-11-03T11:42:00Z',has_file:true},{id:'t3',name:'摄影基础课程 - P2 光线与构图.mp4',platform:'bilibili',status:'paused',size:712917122,downloaded:207618048,speed:0,directory:'学习',created_at:'2026-11-03T11:30:00Z',updated_at:'2026-11-03T11:35:00Z',has_file:false},{id:'t4',name:'旅行影像素材.zip',platform:'pan123',status:'waiting_account',size:4294967296,downloaded:0,speed:0,directory:'媒体/素材',error:'下载地址需要重新授权，请检查账号后继续任务',created_at:'2026-11-03T11:20:00Z',updated_at:'2026-11-03T11:25:00Z',has_file:false}]};
fixture.version='0.2.0-draft';
fixture.platforms.push(...Object.entries({baidu:'百度网盘',xunlei:'迅雷网盘',c139:'139 网盘',pan115:'115 网盘',guangya:'光鸭云盘',lanzou:'蓝奏云',ilanzou:'蓝奏云优享版',douyin:'抖音',xhs:'小红书',kuaishou:'快手',weibo:'微博',acfun:'AcFun',pixiv:'Pixiv',github:'GitHub'}).map(([id,name])=>({id,name,credential:['xunlei','guangya','ilanzou','github'].includes(id)?'token':'cookie',cloud:!['douyin','xhs','kuaishou','weibo','acfun','pixiv'].includes(id)})));
fixture.credential_fields={xunlei:[{key:'device_id',name:'设备 ID',help:'与登录令牌对应的设备 ID；留空保留已有值'},{key:'captcha_token',name:'验证令牌',help:'仅填写平台正常验证后取得的令牌'}]};
export async function startFixture(theme='light'){
 const state=structuredClone(fixture);state.settings.theme=theme;
 const root=resolve(import.meta.dirname,'../../internal/webassets/dist');
 const server=createServer(async(req,res)=>{
  const p=new URL(req.url,'http://local').pathname;res.setHeader('Cache-Control','no-store');
  const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v))};
  if(p==='/api/auth/session')return json({authenticated:true,name:state.settings.name});
  if(p==='/api/state')return json(state);
  if(p==='/api/events'){res.setHeader('Content-Type','text/event-stream');res.write('event: state\ndata: '+JSON.stringify(state)+'\n\n');return}
  if(p==='/api/resolve')return json({session_id:'fixture',platform:'quark',title:'家庭影像资料',parent:'0',page:1,more:false,items:[{id:'f1',name:'纪录片',size:0,directory:true},{id:'f2',name:'山野之间 - 第 01 集.mp4',size:2147483648,directory:false},{id:'f3',name:'拍摄笔记.pdf',size:829440,directory:false}]});
  if(p.startsWith('/api/'))return json({ok:true});
  const file=resolve(root,'.'+(p==='/'?'/index.html':p));if(!file.startsWith(root+'/')){res.statusCode=403;return res.end()}
  try{const b=await readFile(file);res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png'})[extname(file)]||'application/octet-stream');res.end(b)}catch{res.statusCode=404;res.end()}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));return{url:'http://127.0.0.1:'+server.address().port,close:async()=>{server.closeAllConnections();await new Promise(r=>server.close(r))}};
}
