export class APIError extends Error { constructor(public code:string,message:string,public status:number){super(message)} }
export async function api<T>(path:string,method='GET',body?:unknown):Promise<T>{
 const controller=new AbortController();const timeout=setTimeout(()=>controller.abort(),100_000);
 try{const r=await fetch('/api'+path,{method,credentials:'same-origin',headers:method==='GET'?{}:{'Content-Type':'application/json','X-YunX-Request':'1'},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal});const j=await r.json();if(!r.ok)throw new APIError(j.error?.code||'network',j.error?.message||'服务请求失败',r.status);return j as T;}catch(e){if(e instanceof APIError)throw e;throw new APIError('network','无法连接服务，请检查网络后重试',0)}finally{clearTimeout(timeout)}
}
export interface Task {id:string;name:string;platform:string;account_id?:string;status:string;size:number;downloaded:number;speed:number;directory:string;error?:string;error_code?:string;created_at:string;updated_at:string;has_file:boolean}
export interface Account {id:string;name:string;platform:string;status:string;nickname?:string;checked_at?:string;message?:string}
export interface Settings {name:string;concurrency:number;connections:number;speed_limit:number;default_dir:string;resume_on_restart:boolean;theme:string}
export interface Platform {id:string;name:string;credential:string;cloud:boolean}
export interface CredentialField {key:string;name:string;help:string}
export interface State {credential_fields?:Record<string,CredentialField[]>;tasks:Task[];accounts:Account[];platforms:Platform[];settings:Settings;disk:{available:number|null;total:number|null};events:{id:number;task_id:string;kind:string;message:string;created_at:string}[];version:string;updated_at:string;root:string}
export interface FileItem {id:string;name:string;size:number;directory:boolean}
export interface Browse {qualities?:{id:number;name:string}[];quality?:number;notice?:string;session_id:string;platform:string;title:string;items:FileItem[];parent:string;page:number;more:boolean}

export async function uploadTorrent(file:File):Promise<Browse>{
 if(file.size>2*1024*1024)throw new Error('种子文件不能超过 2 MiB');
 const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),90_000);
 try{const r=await fetch('/api/torrents',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/x-bittorrent','X-YunX-Request':'1'},body:file,signal:controller.signal});const j=await r.json();if(!r.ok)throw new APIError(j.error?.code||'torrent',j.error?.message||'种子导入失败',r.status);return j;}finally{clearTimeout(timeout)}
}
