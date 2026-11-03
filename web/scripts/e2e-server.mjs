import{mkdtemp,rm}from'node:fs/promises';import{tmpdir}from'node:os';import{join,resolve}from'node:path';import{execFileSync,spawn}from'node:child_process';
const dir=await mkdtemp(join(tmpdir(),'yunx-e2e-'));const repo=resolve(import.meta.dirname,'../..');const binary=join(dir,'yunx-server');
execFileSync('go',['build','-o',binary,'./cmd/yunx-server'],{cwd:repo,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit'});
const child=spawn(binary,[],{cwd:repo,env:{...process.env,YUNX_ADDR:'127.0.0.1:18080',YUNX_DATA_DIR:join(dir,'data'),YUNX_DOWNLOAD_DIR:join(dir,'downloads'),YUNX_ADMIN_PASSWORD:'test-password-123',YUNX_ALLOW_PRIVATE_DOWNLOADS:'true'},stdio:'inherit'});
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>child.kill(signal));
child.on('exit',async code=>{await rm(dir,{recursive:true,force:true});process.exit(code||0)});
