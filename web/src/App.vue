<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { setTheme } from 'mdui/functions/setTheme.js';
import Icon from './Icon.vue';
import { api, APIError, uploadTorrent, type Account, type Browse, type FileItem, type Settings, type State, type Task } from './api';
import { t, statusLabel, size, date } from './messages';
const routes=['resolve','accounts','downloads','settings'] as const;
type Route=typeof routes[number];
const icons=['link--outlined','account-circle--outlined','download--outlined','settings--outlined'];
const selectedIcons=['link','account-circle','download','settings'];
const pageTitles={resolve:t('navResolve'),accounts:t('navAccounts'),downloads:t('navDownloads'),settings:t('settings')};
const route=ref<Route>('resolve'), ready=ref(false), loggedIn=ref(false), online=ref(false), busy=ref(''), error=ref(''), notice=ref(''), name=ref(t('brand') as string);
const state=ref<State>(), username=ref('admin'), password=ref('');
let liveRevision=0;
let stream:EventSource|undefined, noticeTimer:ReturnType<typeof setTimeout>|undefined;
const settings=reactive<Settings>({name:'云析',concurrency:2,connections:4,speed_limit:0,default_dir:'',resume_on_restart:true,theme:'auto'}), settingsDirty=ref(false), limitMiB=ref(0);
const link=ref(''), passcode=ref(''), accountID=ref(''), directory=ref(''), browse=ref<Browse>(), selectedFiles=ref<string[]>([]), trail=ref<{id:string;name:string}[]>([]);
const query=ref(''), filter=ref('all'), selectedTasks=ref<string[]>([]), accountOpen=ref(false), accountForm=reactive({id:'',platform:'quark',name:'',credential:'',credentials:{} as Record<string,string>});
const torrentInput=ref<HTMLInputElement>(),previewTask=ref<Task>();
const credentialFields=computed(()=>state.value?.credential_fields?.[accountForm.platform]||[]);
const previewKind=(task:Task)=>/\.(mp4|webm|mov|m4v)$/i.test(task.name)?'video':/\.(mp3|m4a|ogg|wav|flac)$/i.test(task.name)?'audio':/\.(jpg|jpeg|png|gif|webp|avif)$/i.test(task.name)?'image':'';
const currentPassword=ref(''), newPassword=ref(''), repeatPassword=ref('');
const confirmOpen=ref(false), confirmType=ref(''), confirmID=ref(''), deleteFile=ref(false);
const directoryOpen=ref(false), directoryPath=ref(''), directories=ref<string[]>([]), newFolder=ref('');
const val=(e:Event)=>String((e.target as HTMLInputElement).value);
const checked=(e:Event)=>Boolean((e.target as HTMLInputElement).checked);
const platformIcons: Record<string,string>={quark:'/icons/quark.png',uc:'/icons/uc.png',pan123:'/icons/pan123.png',bilibili:'/icons/bilibili.png',baidu:'/icons/baidu.png',xunlei:'/icons/xunlei.png',c139:'/icons/c139.png',pan115:'/icons/pan115.png',guangya:'/icons/guangya.png',lanzou:'/icons/lanzou.png',ilanzou:'/icons/ilanzou.png',douyin:'/icons/douyin.png',xhs:'/icons/xhs.png',kuaishou:'/icons/kuaishou.png',weibo:'/icons/weibo.png',acfun:'/icons/acfun.png',pixiv:'/icons/pixiv.png',github:'/icons/github.png'};
const platformName=(id:string)=>state.value?.platforms.find(x=>x.id===id)?.name||({direct:'HTTP / HLS',bt:'BitTorrent',ed2k:'ed2k'}[id as 'direct'|'bt'|'ed2k']||id);
const tasks=computed(()=>state.value?.tasks||[]), accounts=computed(()=>state.value?.accounts||[]);
const running=computed(()=>tasks.value.filter(x=>['resolving','downloading','merging','pausing'].includes(x.status)));
const filtered=computed(()=>tasks.value.filter(x=>(filter.value==='all'||filter.value==='active'&&['queued','resolving','downloading','merging','pausing'].includes(x.status)||filter.value==='completed'&&x.status==='completed'||filter.value==='failed'&&['failed','waiting_account'].includes(x.status))&&x.name.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())));
const totalSpeed=computed(()=>tasks.value.reduce((n,x)=>n+x.speed,0));
const canPause=(x:Task)=>['queued','resolving','downloading'].includes(x.status);
const canResume=(x:Task)=>['paused','failed','waiting_account'].includes(x.status);
const canRemove=(x:Task)=>!['resolving','downloading','merging','pausing'].includes(x.status);
const percent=(x:Task)=>x.size>0?Math.min(1,x.downloaded/x.size):undefined;
function go(r:Route){route.value=r;location.hash=r;error.value=''}
function readHash(){const h=location.hash.slice(1) as Route;route.value=routes.includes(h)?h:'resolve'}
function apply(v:State){state.value=v;name.value=v.settings.name;document.title=`${name.value} · NAS`;if(!settingsDirty.value){Object.assign(settings,v.settings);limitMiB.value=v.settings.speed_limit/1048576}setTheme(v.settings.theme as 'auto'|'light'|'dark')}
function announce(message:string){notice.value=message;if(noticeTimer)clearTimeout(noticeTimer);noticeTimer=setTimeout(()=>notice.value='',5000)}
function showError(e:unknown){error.value=e instanceof Error?e.message:t('networkError');if(e instanceof APIError&&e.status===401){loggedIn.value=false;stream?.close()}}
async function run(key:string,fn:()=>Promise<void>){if(busy.value)return;busy.value=key;error.value='';try{await fn()}catch(e){showError(e)}finally{busy.value=''}}
async function refresh(){const revision=liveRevision;const next=await api<State>('/state');if(revision===liveRevision)apply(next);online.value=true}
function connect(){stream?.close();stream=new EventSource('/api/events');stream.addEventListener('state',e=>{try{const next=JSON.parse((e as MessageEvent).data);liveRevision++;apply(next);online.value=true}catch{online.value=false}});stream.addEventListener('expired',()=>{stream?.close();loggedIn.value=false;online.value=false;error.value='登录已过期，请重新登录。'});stream.onerror=()=>{stream?.close();online.value=false}}
async function reconnect(){await run('refresh',async()=>{await refresh();connect()})}
async function login(){await run('login',async()=>{await api('/auth/login','POST',{username:username.value,password:password.value});password.value='';loggedIn.value=true;await refresh();directory.value=settings.default_dir;connect()})}
async function logout(){await run('logout',async()=>{await api('/auth/logout','POST',{});stream?.close();loggedIn.value=false;online.value=false;state.value=undefined})}
async function parse(cloud=false,id=accountID.value){await run('parse',async()=>{const v=await api<Browse>('/resolve','POST',{text:link.value,password:passcode.value,account_id:id,cloud});browse.value=v;selectedFiles.value=[];trail.value=[{id:'0',name:v.title||t('rootDirectory')}];directory.value=settings.default_dir;go('resolve')})}
async function openFolder(item:FileItem){await run('browse',async()=>{const v=await api<Browse>('/browse','POST',{session_id:browse.value!.session_id,parent:item.id,page:1});browse.value=v;selectedFiles.value=[];trail.value.push({id:item.id,name:item.name})})}
async function backFolder(){if(trail.value.length<2)return;await run('browse',async()=>{const parent=trail.value.at(-2)!;browse.value=await api<Browse>('/browse','POST',{session_id:browse.value!.session_id,parent:parent.id,page:1});trail.value.pop();selectedFiles.value=[]})}
async function loadMore(){await run('more',async()=>{const v=await api<Browse>('/browse','POST',{session_id:browse.value!.session_id,parent:browse.value!.parent,page:browse.value!.page+1});const seen=new Set(browse.value!.items.map(x=>x.id));browse.value={...v,items:[...browse.value!.items,...v.items.filter(x=>!seen.has(x.id))]}})}
async function importTorrent(event:Event){const input=event.target as HTMLInputElement;const file=input.files?.[0];if(!file)return;await run('torrent',async()=>{const v=await uploadTorrent(file);browse.value=v;selectedFiles.value=[];trail.value=[{id:'0',name:v.title}];directory.value=settings.default_dir});input.value=''}
async function changeQuality(event:Event){await run('quality',async()=>{browse.value=await api<Browse>('/quality','POST',{session_id:browse.value!.session_id,quality:Number(val(event))});selectedFiles.value=[]})}
async function cleanup(){await run('cleanup',async()=>{const result=await api<{cleaned:number;pending:number}>('/cleanup','POST',{});announce(`已清理 ${result.cleaned} 项，剩余 ${result.pending} 项`);await refresh()})}
function toggle(list:string[],id:string,on:boolean){return on?[...new Set([...list,id])]:list.filter(x=>x!==id)}
async function enqueue(){await run('enqueue',async()=>{await api('/tasks','POST',{session_id:browse.value!.session_id,item_ids:selectedFiles.value,directory:directory.value});selectedFiles.value=[];await refresh();go('downloads');announce(t('queuedSuccess'))})}
async function action(id:string,action:string){await run(id,async()=>{await api(`/tasks/${id}/action`,'POST',{action,delete_file:false});await refresh()})}
async function bulk(actionName:string){await run('bulk',async()=>{for(const task of tasks.value.filter(x=>selectedTasks.value.includes(x.id)&&(actionName==='pause'?canPause(x):canResume(x)))){await api(`/tasks/${task.id}/action`,'POST',{action:actionName,delete_file:false})}selectedTasks.value=[];await refresh()})}
function ask(type:string,id:string){confirmType.value=type;confirmID.value=id;deleteFile.value=false;confirmOpen.value=true}
async function confirmAction(){await run('confirm',async()=>{if(confirmType.value==='account'){await api(`/accounts/${confirmID.value}`,'DELETE')}else{await api(`/tasks/${confirmID.value}/action`,'POST',{action:confirmType.value,delete_file:deleteFile.value})}confirmOpen.value=false;await refresh();announce(t('operationSuccess'))})}
function editAccount(a?:Account,platform='quark'){Object.assign(accountForm,{id:a?.id||'',platform:a?.platform||platform,name:a?.name||'',credential:'',credentials:{}});accountOpen.value=true}
async function saveAccount(){await run('account-save',async()=>{await api('/accounts','POST',accountForm);accountForm.credential='';accountForm.credentials={};accountOpen.value=false;await refresh();announce(t('accountSaved'))})}
async function checkAccount(id:string){await run(id,async()=>{await api(`/accounts/${id}/check`,'POST',{});await refresh()})}
function dirty(){settingsDirty.value=true}
async function saveSettings(){await run('settings',async()=>{const saved=await api<Settings>('/settings','PUT',{...settings,speed_limit:Math.round(limitMiB.value*1048576)});settingsDirty.value=false;Object.assign(settings,saved);await refresh();announce(t('settingsSaved'));settingEditor.value=undefined})}
async function changePassword(){await run('password',async()=>{if(newPassword.value!==repeatPassword.value)throw new Error(t('passwordMismatch'));await api('/auth/password','POST',{current:currentPassword.value,password:newPassword.value});currentPassword.value='';newPassword.value='';repeatPassword.value='';stream?.close();loggedIn.value=false;settingEditor.value=undefined;announce(t('operationSuccess'))})}
async function readDirectories(path:string){const v=await api<{path:string;directories:string[]}>('/directories?path='+encodeURIComponent(path));directoryPath.value=v.path;directories.value=v.directories}
async function chooseDirectory(){await run('directory',async()=>{await readDirectories(directory.value);directoryOpen.value=true})}
async function navigateDirectory(path:string){await run('directory',()=>readDirectories(path))}
async function createFolder(){await run('folder',async()=>{if(!newFolder.value||/[\\/]/.test(newFolder.value))throw new Error('请输入单层文件夹名称。');await api('/directories','POST',{path:[directoryPath.value,newFolder.value].filter(Boolean).join('/')});newFolder.value='';await readDirectories(directoryPath.value)})}
watch(route,()=>{selectedTasks.value=[]});
onMounted(async()=>{readHash();window.addEventListener('hashchange',readHash);try{const s=await api<{authenticated:boolean;name:string}>('/auth/session');name.value=s.name;document.title=`${s.name} · NAS`;loggedIn.value=s.authenticated;if(s.authenticated){await refresh();directory.value=settings.default_dir;connect()}}catch(e){showError(e)}finally{ready.value=true}});
onUnmounted(()=>{stream?.close();if(noticeTimer)clearTimeout(noticeTimer);window.removeEventListener('hashchange',readHash)});

type SettingKey='connections'|'directory'|'concurrency'|'limit'|'resume'|'theme'|'name'|'password'|'cleanup';
const settingEditor=ref<SettingKey>();
const remainingPlatforms=computed(()=>state.value?.platforms.filter(p=>!accounts.value.some(a=>a.platform===p.id))||[]);
const platformGroups=computed(()=>[
 {name:t('cloudPlatforms'),items:remainingPlatforms.value.filter(p=>p.cloud&&p.id!=='github')},
 {name:t('mediaPlatforms'),items:remainingPlatforms.value.filter(p=>!p.cloud)},
 {name:'GitHub',items:remainingPlatforms.value.filter(p=>p.id==='github')},
].filter(group=>group.items.length));
const settingGroups=computed(()=>{
 const v=state.value?.settings||settings;
 return [
  {title:t('downloadSettings'),items:[
   {key:'connections',icon:'tune',title:t('connectionsTitle'),detail:`${v.connections} ${t('connectionsUnit')}`},
   {key:'directory',icon:'folder--outlined',title:t('saveDirectory'),detail:'/downloads/'+v.default_dir},
   {key:'concurrency',icon:'layers--outlined',title:t('concurrencyTitle'),detail:`${t('concurrencyPrefix')} ${v.concurrency} ${t('concurrencySuffix')}`},
   {key:'limit',icon:'speed',title:t('limitTitle'),detail:v.speed_limit?size(v.speed_limit)+'/s':t('unlimited')},
   {key:'resume',icon:'restart-alt',title:t('resumeRestart'),detail:(v.resume_on_restart?t('enabled'):t('disabled'))+' · '+t('resumeRestartHelp')},
   {key:'cleanup',icon:'cleaning-services--outlined',title:t('cleanup'),detail:t('cleanupHelp')},
  ]},
  {title:t('general'),items:[
   {key:'theme',icon:'palette--outlined',title:t('theme'),detail:t(v.theme as 'auto'|'light'|'dark')},
   {key:'name',icon:'badge--outlined',title:t('instanceName'),detail:v.name},
  ]},
  {title:t('security'),items:[{key:'password',icon:'lock--outlined',title:t('passwordTitle'),detail:t('passwordHelp')}]},
 ];
});
const settingTitle=computed(()=>settingGroups.value.flatMap(g=>g.items).find(x=>x.key===settingEditor.value)?.title||'');
function editSetting(key:string){
 if(busy.value)return;
 if(state.value){Object.assign(settings,state.value.settings);limitMiB.value=state.value.settings.speed_limit/1048576}
 settingsDirty.value=false;error.value='';settingEditor.value=key as SettingKey;
}
function closeSetting(){
 settingEditor.value=undefined;settingsDirty.value=false;
 if(state.value){Object.assign(settings,state.value.settings);limitMiB.value=state.value.settings.speed_limit/1048576}
 currentPassword.value='';newPassword.value='';repeatPassword.value='';
}
</script>

<template>
 <div v-if="!ready" class="initializing" role="status"><mdui-circular-progress/><p>{{t('initializing')}}</p></div>
 <main v-else-if="!loggedIn" class="login-page">
  <form class="login-panel" @submit.prevent="login">
   <div class="brand"><img src="/icons/yunx.png" alt="" width="48" height="48"/><span>{{name}}</span></div>
   <h1>{{t('loginTitle')}}</h1><p class="muted">{{t('loginDescription')}}</p>
   <div v-if="error" class="alert error" role="alert">{{error}}</div>
   <mdui-text-field variant="outlined" :label="t('username')" :value="username" autocomplete="username" required @input="username=val($event)"/>
   <mdui-text-field variant="outlined" :label="t('password')" :value="password" type="password" autocomplete="current-password" toggle-password required @input="password=val($event)"/>
   <mdui-button type="submit" :loading="busy==='login'" :disabled="!!busy">{{t('login')}}</mdui-button>
   <p class="helper">{{t('loginHelp')}}</p>
  </form>
 </main>
 <div v-else class="app-shell">
  <aside class="sidebar">
   <a class="rail-brand" href="#resolve" :aria-label="name" :title="name"><img src="/icons/yunx.png" alt="" width="32" height="32"/></a>
   <nav aria-label="主导航">
    <a v-for="(item,i) in routes" :key="item" :href="'#'+item" :class="{active:route===item}" :aria-label="t(item)" :title="t(item)" :aria-current="route===item?'page':undefined" @click="go(item)">
     <span class="nav-icon"><Icon :name="route===item?selectedIcons[i]!:icons[i]!"/></span><span class="nav-label">{{pageTitles[item]}}</span>
    </a>
   </nav>
   <mdui-button-icon class="rail-logout" v-control-label="t('logout')" :disabled="!!busy" @click="logout"><Icon name="logout"/></mdui-button-icon>
  </aside>
  <main class="main-content" :class="'page-'+route">
   <div class="app-topbar">
    <a class="app-name" href="#resolve"><img src="/icons/yunx.png" alt="" width="24" height="24"/><span>{{name}}</span><small>NAS</small></a>
    <div class="heading-actions">
     <span class="connection" :title="online?t('connected'):t('disconnected')"><span class="status-dot" :class="{offline:!online}"></span><span>{{online?t('connected'):t('disconnected')}}</span></span>
     <mdui-button-icon v-control-label="t('refresh')" :disabled="!!busy" @click="reconnect"><Icon name="refresh"/></mdui-button-icon>
     <mdui-button-icon v-if="route==='downloads'" v-control-label="t('newDownload')" @click="go('resolve')"><Icon name="add"/></mdui-button-icon>
     <mdui-button-icon v-if="route==='accounts'" v-control-label="t('addAccount')" @click="editAccount()"><Icon name="add"/></mdui-button-icon>
    </div>
   </div>
   <header class="page-heading"><h1>{{pageTitles[route]}}</h1><p v-if="route==='resolve'||route==='accounts'" class="page-description">{{t(route==='resolve'?'resolveDescription':'accountsDescription')}}</p></header>
   <div v-if="!online" class="alert offline-alert" role="status"><span>{{t('offline')}}</span><mdui-button variant="text" @click="reconnect" :disabled="!!busy">{{t('reconnect')}}</mdui-button></div>
   <div v-if="error&&!accountOpen&&!settingEditor&&!confirmOpen&&!directoryOpen" class="alert error" role="alert"><Icon name="error-outline"/><span>{{error}}</span><mdui-button-icon v-control-label="t('close')" @click="error=''"><Icon name="close"/></mdui-button-icon></div>

   <template v-if="route==='resolve'">
    <form class="resolve-form" @submit.prevent="parse()">
     <mdui-text-field class="link-field" variant="outlined" :label="t('link')" :placeholder="t('linkPlaceholder')" :value="link" required autosize min-rows="3" max-rows="6" clearable @input="link=val($event)"><Icon slot="icon" name="link--outlined"/></mdui-text-field>
     <mdui-text-field variant="outlined" :label="t('passcode')" :value="passcode" clearable @input="passcode=val($event)"/>
     <mdui-button class="parse-button" type="submit" :loading="busy==='parse'" :disabled="!link.trim()||!!busy||!online">{{busy==='parse'?t('parsing'):t('parse')}}</mdui-button>
     <div class="resolve-options">
      <details class="account-choice"><summary><Icon name="account-circle--outlined"/><span>{{t('accountOptions')}}</span><span class="muted">{{accounts.find(a=>a.id===accountID)?.name||t('guest')}}</span><Icon name="expand-more"/></summary><mdui-select variant="outlined" :label="t('useAccount')" :value="accountID" @change="accountID=val($event)"><mdui-menu-item value="">{{t('guest')}}</mdui-menu-item><mdui-menu-item v-for="a in accounts" :key="a.id" :value="a.id">{{platformName(a.platform)}} · {{a.name}}</mdui-menu-item></mdui-select></details>
      <div class="resolve-extras"><mdui-button variant="tonal" :disabled="!!busy||!online" :loading="busy==='torrent'" @click="torrentInput?.click()"><Icon slot="icon" name="add"/>{{t('torrentImport')}}</mdui-button><input ref="torrentInput" type="file" accept=".torrent,application/x-bittorrent" hidden @change="importTorrent"/><span class="helper">{{t('torrentHelp')}}</span></div>
      <details class="supported-platforms"><summary>{{t('platformList')}} · {{state?.platforms.length||0}}<Icon name="expand-more"/></summary><p class="helper">{{t('supported')}}</p><div class="supported-list"><span v-for="p in state?.platforms" :key="p.id"><img v-if="platformIcons[p.id]" :src="platformIcons[p.id]" alt="" width="20" height="20"/>{{p.name}}</span></div></details>
     </div>
    </form>
    <section class="surface file-browser" :class="{'awaiting-parse':!browse}">
     <div v-if="!browse" class="empty-state compact-empty"><Icon name="folder--outlined"/><div><h2>{{t('parseFirst')}}</h2><p>{{t('parseFirstBody')}}</p></div></div>
     <template v-else>
      <div class="browser-heading"><mdui-button-icon :disabled="trail.length<2||!!busy" v-control-label="t('back')" @click="backFolder"><Icon name="arrow-back"/></mdui-button-icon><h2 :title="trail.map(x=>x.name).join(' / ')">{{trail.at(-1)?.name}}</h2><span class="muted">{{browse.items.length}} {{t('files')}}</span></div>
      <div v-if="browse.qualities?.length" class="quality-field"><mdui-select variant="outlined" :label="t('quality')" :value="String(browse.quality)" :disabled="!!busy" @change="changeQuality"><mdui-menu-item v-for="q in browse.qualities" :key="q.id" :value="String(q.id)">{{q.name}}</mdui-menu-item></mdui-select></div>
      <p v-if="browse.notice" class="parse-notice helper" role="status">{{browse.notice}}</p>
      <div class="browser-select"><mdui-checkbox :checked="!!browse.items.length&&browse.items.every(x=>selectedFiles.includes(x.id))" @change="selectedFiles=checked($event)?browse!.items.map(x=>x.id):[]">{{t('allSelected')}}</mdui-checkbox><span class="muted">{{t('selected')}} {{selectedFiles.length}}</span></div>
      <p v-if="!browse.items.length" class="empty-folder">{{t('emptyFolder')}}</p>
      <div v-for="file in browse.items" :key="file.id" class="browser-row"><mdui-checkbox v-control-label="'选择 '+file.name" :checked="selectedFiles.includes(file.id)" @change="selectedFiles=toggle(selectedFiles,file.id,checked($event))"/><Icon :name="file.directory?'folder--outlined':'description--outlined'"/><button v-if="file.directory" class="folder-link" :disabled="!!busy" @click="openFolder(file)">{{file.name}}</button><span v-else class="browser-name">{{file.name}}</span><span class="numeric muted">{{file.directory?t('folder'):size(file.size)}}</span></div>
      <div v-if="browse.more" class="load-more"><mdui-button variant="text" :disabled="!!busy" @click="loadMore">{{t('loadMore')}}</mdui-button></div>
      <div class="queue-bar"><div class="directory-field"><mdui-text-field variant="outlined" :label="t('saveDirectory')" prefix="/downloads/" :value="directory" @input="directory=val($event)"/><mdui-button-icon v-control-label="t('directoryButton')" @click="chooseDirectory"><Icon name="folder--outlined"/></mdui-button-icon></div><mdui-button :disabled="!selectedFiles.length||!!busy||!online" :loading="busy==='enqueue'" @click="enqueue"><Icon slot="icon" name="download--outlined"/>{{t('addToQueue')}} ({{selectedFiles.length}})</mdui-button></div>
     </template>
    </section>
   </template>

   <template v-if="route==='downloads'">
    <section class="summary-strip" aria-label="下载概况"><span>{{t('totalTasks')}} <strong>{{tasks.length}}</strong></span><span>{{t('running')}} <strong>{{running.length}}</strong></span><span>{{t('totalSpeed')}} <strong>{{size(totalSpeed)}}/s</strong></span><span>{{t('diskAvailable')}} <strong>{{size(state?.disk.available)}}</strong></span></section>
    <section class="task-panel">
     <div class="task-toolbar"><div class="filters" role="group" aria-label="任务筛选"><button v-for="key in ['all','active','completed','failed'] as const" :key="key" :class="{selected:filter===key}" :aria-pressed="filter===key" @click="filter=key">{{t(key)}}</button></div><mdui-text-field variant="outlined" class="task-search" :label="t('searchTasks')" :value="query" clearable @input="query=val($event)"/></div>
     <div v-if="filtered.length" class="selection-row"><mdui-checkbox v-control-label="t('allSelected')" :checked="filtered.every(x=>selectedTasks.includes(x.id))" @change="selectedTasks=checked($event)?filtered.map(x=>x.id):[]">{{t('allSelected')}}</mdui-checkbox><span v-if="selectedTasks.length">{{t('selected')}} {{selectedTasks.length}}</span><div v-if="selectedTasks.length" class="bulk-toolbar"><mdui-button variant="text" :disabled="!!busy||!online" @click="bulk('pause')"><Icon slot="icon" name="pause"/>{{t('pauseSelected')}}</mdui-button><mdui-button variant="text" :disabled="!!busy||!online" @click="bulk('resume')"><Icon slot="icon" name="play-arrow"/>{{t('resumeSelected')}}</mdui-button><mdui-button variant="text" @click="selectedTasks=[]">{{t('cancel')}}</mdui-button></div></div>
     <div v-if="!filtered.length" class="empty-state"><Icon name="download--outlined"/><h2>{{tasks.length?t('noResults'):t('emptyTasks')}}</h2><p>{{tasks.length?t('noResultsBody'):t('emptyTasksBody')}}</p><mdui-button v-if="!tasks.length" @click="go('resolve')"><Icon slot="icon" name="add"/>{{t('newDownload')}}</mdui-button></div>
     <div v-else class="task-table" role="list" aria-label="下载任务">
      <article v-for="task in filtered" :key="task.id" class="task-row surface" role="listitem" :data-task-id="task.id">
       <div class="file-cell"><span class="file-icon"><Icon name="description--outlined"/></span><div><button v-if="task.has_file&&previewKind(task)" class="file-title preview-link" :title="task.name" :aria-label="t('preview')+' '+task.name" @click="previewTask=task">{{task.name}}</button><strong v-else class="file-title" :title="task.name">{{task.name}}</strong><span class="file-meta">{{platformName(task.platform)}} · /downloads/{{task.directory}}</span></div></div>
       <div class="task-actions"><mdui-button-icon v-if="canPause(task)" v-control-label="t('pause')+' '+task.name" :disabled="!!busy||!online" @click="action(task.id,'pause')"><Icon name="pause"/></mdui-button-icon><mdui-button-icon v-if="canResume(task)" v-control-label="t('resume')+' '+task.name" :disabled="!!busy||!online" @click="action(task.id,'resume')"><Icon name="play-arrow"/></mdui-button-icon><mdui-button-icon v-if="task.has_file" :href="'/api/tasks/'+task.id+'/file'" v-control-label="t('downloadToDevice')+' '+task.name"><Icon name="download--outlined"/></mdui-button-icon><mdui-button-icon v-if="canRemove(task)" v-control-label="t('remove')+' '+task.name" :disabled="!!busy||!online" @click="ask('delete',task.id)"><Icon name="delete--outlined"/></mdui-button-icon><mdui-button-icon v-else-if="task.status!=='merging'" v-control-label="t('stop')+' '+task.name" :disabled="!!busy||!online" @click="ask('cancel',task.id)"><Icon name="close"/></mdui-button-icon></div>
       <div class="progress-cell"><div class="progress-label"><span class="status-label" :class="task.status">{{statusLabel(task.status)}}</span><span class="numeric">{{size(task.downloaded)}} / {{task.size>0?size(task.size):t('unknownSize')}}</span><span v-if="task.speed>0" class="speed-cell numeric">{{size(task.speed)}}/s</span></div><mdui-linear-progress v-if="['downloading','resolving','paused','pausing','queued'].includes(task.status)" :value="percent(task)" :aria-label="task.name+' '+t('progress')"/></div>
       <p v-if="task.error" class="task-error">{{task.error}}</p>
       <mdui-checkbox class="task-selection" v-control-label="'选择 '+task.name" :checked="selectedTasks.includes(task.id)" @change="selectedTasks=toggle(selectedTasks,task.id,checked($event))"/>
      </article>
     </div>
    </section>
    <details class="activity surface"><summary>{{t('logTitle')}}<span class="muted">{{state?.events.length||0}}</span><Icon name="expand-more"/></summary><p v-if="!state?.events.length" class="muted">{{t('logEmpty')}}</p><ol v-else><li v-for="event in state?.events.slice(0,30)" :key="event.id"><time :datetime="event.created_at">{{date(event.created_at)}}</time><span>{{event.message}}</span></li></ol></details>
   </template>

   <template v-if="route==='accounts'">
    <section class="account-list">
     <h2 v-if="accounts.length" class="group-title">{{t('savedAccounts')}}</h2>
     <div v-else class="accounts-empty"><p>{{t('noAccounts')}}</p><p class="helper">{{t('noAccountsBody')}}</p></div>
     <article v-for="a in accounts" :key="a.id" class="account-row surface">
      <div class="platform-mark"><img v-if="platformIcons[a.platform]" :src="platformIcons[a.platform]" alt="" width="32" height="32"/><Icon v-else name="cloud--outlined"/></div>
      <div class="account-info"><h2>{{a.name}}</h2><p class="muted">{{platformName(a.platform)}}<template v-if="a.nickname"> · {{a.nickname}}</template></p><p class="helper">{{a.message||t('accountUnchecked')}}</p><span class="status-label" :class="a.status">{{statusLabel(a.status)}}</span></div>
      <div class="account-actions"><mdui-dropdown placement="bottom-end"><mdui-button-icon slot="trigger" v-control-label="t('accountActions')+' '+a.name"><Icon name="more-vert"/></mdui-button-icon><mdui-menu role="menu"><mdui-menu-item role="menuitem" :disabled="!!busy||!online" @click="checkAccount(a.id)"><Icon slot="icon" name="refresh"/>{{t('check')}}</mdui-menu-item><mdui-menu-item role="menuitem" v-if="state?.platforms.find(p=>p.id===a.platform)?.cloud" :disabled="!!busy||!online" @click="parse(true,a.id)"><Icon slot="icon" name="folder--outlined"/>{{t('browseCloud')}}</mdui-menu-item><mdui-menu-item role="menuitem" @click="editAccount(a)"><Icon slot="icon" name="edit--outlined"/>{{t('edit')}}</mdui-menu-item><mdui-menu-item role="menuitem" :disabled="!!busy||!online" @click="ask('account',a.id)"><Icon slot="icon" name="delete--outlined"/>{{t('deleteAccount')}}</mdui-menu-item></mdui-menu></mdui-dropdown></div>
     </article>
    </section>
    <section v-for="group in platformGroups" :key="group.name" class="platform-group"><h2 class="group-title">{{group.name}}</h2><div class="grouped-list"><article v-for="p in group.items" :key="p.id" class="platform-row"><div class="platform-mark"><img v-if="platformIcons[p.id]" :src="platformIcons[p.id]" alt="" width="32" height="32"/></div><div class="platform-info"><h3>{{p.name}}</h3><p>{{t('platformLoginHint')}}</p></div><mdui-button variant="text" v-control-label="t('addAccount')+' '+p.name" @click="editAccount(undefined,p.id)">{{t('addPlatform')}}</mdui-button></article></div></section>
    <p class="accounts-note"><Icon name="shield--outlined"/>{{t('privacy')}}</p>
   </template>

   <template v-if="route==='settings'">
    <section v-for="group in settingGroups" :key="group.title" class="settings-section"><h2 class="group-title">{{group.title}}</h2><div class="grouped-list"><button v-for="item in group.items" :key="item.key" class="setting-row" :data-setting="item.key" :disabled="!!busy" @click="editSetting(item.key)"><Icon :name="item.icon"/><span class="setting-copy"><strong>{{item.title}}</strong><span>{{item.detail}}</span></span><Icon name="chevron-right"/></button></div></section>
    <div class="settings-footer"><span>{{name}} · {{t('version')}} {{state?.version}}</span><mdui-button variant="text" :disabled="!!busy" @click="logout"><Icon slot="icon" name="logout"/>{{t('logout')}}</mdui-button></div>
   </template>
  </main>
  <nav class="mobile-nav" aria-label="移动导航"><a v-for="(item,i) in routes" :key="item" :href="'#'+item" :class="{active:route===item}" :aria-label="t(item)" :aria-current="route===item?'page':undefined" @click="go(item)"><span class="nav-icon"><Icon :name="route===item?selectedIcons[i]!:icons[i]!"/></span><span>{{pageTitles[item]}}</span></a></nav>
 </div>
 <mdui-dialog :open="!!settingEditor" :headline="settingTitle" close-on-esc @closed.self="closeSetting">
  <form id="setting-form" class="dialog-form settings-fields" @submit.prevent="settingEditor==='password'?changePassword():saveSettings()">
   <mdui-text-field v-if="settingEditor==='name'" variant="outlined" :label="t('instanceName')" :value="settings.name" maxlength="60" required @input="settings.name=val($event);dirty()"/>
   <mdui-select v-if="settingEditor==='theme'" variant="outlined" :label="t('theme')" :value="settings.theme" @change="settings.theme=val($event);dirty()"><mdui-menu-item v-for="v in ['auto','light','dark'] as const" :key="v" :value="v">{{t(v)}}</mdui-menu-item></mdui-select>
   <mdui-text-field v-if="settingEditor==='directory'" variant="outlined" :label="t('saveDirectory')" prefix="/downloads/" :value="settings.default_dir" :helper="t('directoryHint')" @input="settings.default_dir=val($event);dirty()"/>
   <mdui-text-field v-if="settingEditor==='concurrency'" variant="outlined" :label="t('concurrency')" type="number" min="1" max="8" required :value="String(settings.concurrency)" @input="settings.concurrency=Number(val($event));dirty()"/>
   <mdui-text-field v-if="settingEditor==='connections'" variant="outlined" :label="t('connections')" type="number" min="1" max="32" required :value="String(settings.connections)" @input="settings.connections=Number(val($event));dirty()"/>
   <mdui-text-field v-if="settingEditor==='limit'" variant="outlined" :label="t('limit')" type="number" min="0" step="any" required :value="String(limitMiB)" :helper="t('limitHint')" @input="limitMiB=Number(val($event));dirty()"/>
   <label v-if="settingEditor==='resume'" class="switch-field"><span>{{t('resumeRestart')}}<small>{{t('resumeRestartHelp')}}</small></span><mdui-switch v-control-label="t('resumeRestart')" :checked="settings.resume_on_restart" @change="settings.resume_on_restart=checked($event);dirty()"/></label>
   <p v-if="settingEditor==='cleanup'" class="helper">{{t('cleanupHelp')}}</p>
   <template v-if="settingEditor==='password'"><p class="helper">{{t('passwordHelp')}}</p><mdui-text-field variant="outlined" :label="t('currentPassword')" type="password" autocomplete="current-password" :value="currentPassword" required @input="currentPassword=val($event)"/><mdui-text-field variant="outlined" :label="t('newPassword')" type="password" autocomplete="new-password" minlength="12" :value="newPassword" required @input="newPassword=val($event)"/><mdui-text-field variant="outlined" :label="t('repeatPassword')" type="password" autocomplete="new-password" :value="repeatPassword" required @input="repeatPassword=val($event)"/></template>
   <p v-if="error&&settingEditor" class="inline-error" role="alert">{{error}}</p>
  </form>
  <mdui-button slot="action" variant="text" @click="closeSetting">{{t('cancel')}}</mdui-button>
  <mdui-button v-if="settingEditor==='cleanup'" slot="action" :loading="busy==='cleanup'" :disabled="!!busy||!online" @click="cleanup">{{t('cleanup')}}</mdui-button>
  <mdui-button v-else slot="action" type="submit" form="setting-form" :loading="busy==='settings'||busy==='password'" :disabled="!!busy||!online||(settingEditor!=='password'&&!settingsDirty)">{{settingEditor==='password'?t('changePassword'):t('saveSettings')}}</mdui-button>
 </mdui-dialog>
 <mdui-dialog :open="accountOpen" :headline="accountForm.id?t('editAccount'):t('addAccount')" close-on-overlay-click close-on-esc @closed.self="accountOpen=false"><form id="account-form" class="dialog-form" @submit.prevent="saveAccount"><mdui-select variant="outlined" :label="t('platform')" :disabled="!!accountForm.id" :value="accountForm.platform" @change="accountForm.platform=val($event);accountForm.credential='';accountForm.credentials={}"><mdui-menu-item v-for="p in state?.platforms" :key="p.id" :value="p.id">{{p.name}}</mdui-menu-item></mdui-select><mdui-text-field variant="outlined" :label="t('accountName')" :value="accountForm.name" required maxlength="80" @input="accountForm.name=val($event)"/><mdui-text-field variant="outlined" :label="t('credential')" type="password" toggle-password autocomplete="off" :value="accountForm.credential" :required="!accountForm.id" :helper="accountForm.id?t('credentialKeep'):t('credentialHint')" @input="accountForm.credential=val($event)"/><mdui-text-field variant="outlined" v-for="field in credentialFields" :key="field.key" :label="field.name" :helper="field.help" type="password" toggle-password autocomplete="off" :value="accountForm.credentials[field.key]||''" @input="accountForm.credentials[field.key]=val($event)"/><p v-if="error&&accountOpen" class="inline-error" role="alert">{{error}}</p></form><mdui-button slot="action" variant="text" @click="accountOpen=false">{{t('cancel')}}</mdui-button><mdui-button slot="action" form="account-form" type="submit" :disabled="!!busy" :loading="busy==='account-save'">{{t('save')}}</mdui-button></mdui-dialog>
 <mdui-dialog :open="confirmOpen" :headline="confirmType==='account'?t('accountDeleteTitle'):confirmType==='delete'?t('deleteTitle'):t('cancelTitle')" close-on-esc @closed.self="confirmOpen=false"><p>{{confirmType==='account'?t('accountDeleteHelp'):confirmType==='delete'?t('deleteHelp'):t('cancelHelp')}}</p><template v-if="confirmType==='delete'"><mdui-checkbox :checked="deleteFile" @change="deleteFile=checked($event)">{{t('deleteFile')}}</mdui-checkbox><p v-if="deleteFile" class="inline-error">{{t('deleteFileHelp')}}</p></template><p v-if="error&&confirmOpen" class="inline-error" role="alert">{{error}}</p><mdui-button slot="action" variant="text" @click="confirmOpen=false">{{t('cancel')}}</mdui-button><mdui-button slot="action" :disabled="!!busy" @click="confirmAction">{{t('confirm')}}</mdui-button></mdui-dialog>
 <mdui-dialog :open="directoryOpen" :headline="t('selectDirectory')" close-on-esc @closed.self="directoryOpen=false"><div class="directory-browser"><div class="directory-current"><mdui-button-icon v-control-label="t('back')" :disabled="!directoryPath||!!busy" @click="navigateDirectory(directoryPath.split('/').slice(0,-1).join('/'))"><Icon name="arrow-back"/></mdui-button-icon><span>/downloads/{{directoryPath}}</span></div><button v-for="d in directories" :key="d" class="directory-option" :disabled="!!busy" @click="navigateDirectory(d)"><Icon name="folder--outlined"/>{{d.split('/').at(-1)}}</button><p v-if="!directories.length" class="muted">{{t('emptyFolder')}}</p><form class="form-row" @submit.prevent="createFolder"><mdui-text-field variant="outlined" :label="t('folderName')" :value="newFolder" required @input="newFolder=val($event)"/><mdui-button type="submit" variant="text" :disabled="!!busy">{{t('newFolder')}}</mdui-button></form><p v-if="error&&directoryOpen" class="inline-error" role="alert">{{error}}</p></div><mdui-button slot="action" variant="text" @click="directoryOpen=false">{{t('cancel')}}</mdui-button><mdui-button slot="action" @click="directory=directoryPath;directoryOpen=false">{{t('useDirectory')}}</mdui-button></mdui-dialog>
 <mdui-dialog :open="!!previewTask" :headline="previewTask?.name" close-on-esc close-on-overlay-click @closed.self="previewTask=undefined"><div v-if="previewTask" class="media-preview"><img v-if="previewKind(previewTask)==='image'" :src="'/api/tasks/'+previewTask.id+'/file?preview=1'" :alt="previewTask.name"/><video v-else-if="previewKind(previewTask)==='video'" controls playsinline preload="metadata" :src="'/api/tasks/'+previewTask.id+'/file?preview=1'">{{t('previewUnsupported')}}</video><audio v-else controls preload="metadata" :src="'/api/tasks/'+previewTask.id+'/file?preview=1'">{{t('previewUnsupported')}}</audio><p class="helper">{{t('previewUnsupported')}}</p></div><mdui-button slot="action" variant="text" @click="previewTask=undefined">{{t('close')}}</mdui-button><mdui-button v-if="previewTask" slot="action" :href="'/api/tasks/'+previewTask.id+'/file'">{{t('downloadToDevice')}}</mdui-button></mdui-dialog>
 <div v-if="notice" class="toast" role="status">{{notice}}</div>
</template>
