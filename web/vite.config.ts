import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
 plugins:[vue({template:{compilerOptions:{isCustomElement:tag=>tag.startsWith('mdui-')}}})],
 server:{port:5173,proxy:{'/api':{target:'http://127.0.0.1:8080',changeOrigin:false},'/health':'http://127.0.0.1:8080'}},
 build:{outDir:'../internal/webassets/dist',emptyOutDir:true},
});
