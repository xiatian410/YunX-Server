import { createApp } from 'vue';
import 'mdui/mdui.css';
import 'mdui/components/button.js';
import 'mdui/components/button-icon.js';
import 'mdui/components/text-field.js';
import 'mdui/components/select.js';
import 'mdui/components/menu-item.js';
import 'mdui/components/checkbox.js';
import 'mdui/components/dialog.js';
import 'mdui/components/switch.js';
import 'mdui/components/linear-progress.js';
import 'mdui/components/circular-progress.js';
import 'mdui/components/dropdown.js';
import 'mdui/components/menu.js';
import App from './App.vue';
import './theme.css';
import './style.css';
// MDUI 2 custom elements do not forward host aria-label to their native control.
async function labelControl(el:HTMLElement,binding:{value:string}){
 await (el as HTMLElement & {updateComplete?:Promise<unknown>}).updateComplete;
 const control=el.shadowRoot?.querySelector('button,a,input');
 if(control)control.setAttribute('aria-label',binding.value);
 el.setAttribute('title',binding.value);
}
createApp(App).directive('control-label',{mounted:labelControl,updated:labelControl}).mount('#app');
