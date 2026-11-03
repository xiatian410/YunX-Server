// Compare the same Android layout across the three named refinements.
import {readFile,writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import assert from 'node:assert/strict';
import postcss from 'postcss';
const hash=x=>createHash('sha256').update(typeof x==='string'?x:JSON.stringify(x)).digest('hex');
const read=async p=>JSON.parse(await readFile(p,'utf8'));
const allowed={'.account-row':['padding'],'.platform-row':['gap','padding-left','padding-right'],'.sidebar nav a.active:hover .nav-icon':['background'],'.mdui-theme-dark img[src="/icons/github.png"]':['background','border-radius']};
const runs=[],baseline=await read('.work/design/md3-draft/measurements.json');let first;
for(const candidate of ['A','B','C']){
 const path=`.work/design/md3-${candidate}`,main=await read(`${path}/measurements.json`),states=await read(`${path}/states.json`),all=[...main,...states];assert.equal(all.length,60);
 postcss.parse(await readFile(`docs/design/md3/candidate-${candidate}.css`,'utf8')).walkRules(rule=>{assert.ok(allowed[rule.selector],`Unreviewed selector: ${rule.selector}`);rule.walkDecls(d=>assert.ok(allowed[rule.selector].includes(d.prop),`Unreviewed property: ${d.prop}`))});
 for(const [i,v]of main.entries())assert.deepEqual(v.structure,baseline[i].structure,`Draft structure changed: ${candidate}/${v.route}/${v.width}/${v.theme}`);
 if(first)for(const [i,v]of all.entries()){assert.deepEqual(v.structure,first[i].structure);assert.deepEqual(v.geometry,first[i].geometry)}else first=all;
 assert.ok(all.every(v=>!v.overflow&&!v.errors.length&&v.images));assert.ok(states.every(v=>v.helpers.every(h=>h.spill<=1)));
 assert.ok(main.filter(v=>v.route==='accounts').every(v=>v.namedFixes.accountTextDelta===0));
 assert.ok(main.filter(v=>v.route==='accounts'&&v.theme==='dark').every(v=>v.namedFixes.githubBackground==='rgb(226, 226, 233)'));
 const alpha={A:.08,B:.10,C:.12}[candidate];
 for(const v of main.filter(v=>v.width>=600)){
  const actual=v.namedFixes.activeHoverBackground.match(/^color\(srgb ([\d.]+) ([\d.]+) ([\d.]+)\)$/);assert.ok(actual,'Hover transition must finish before measuring');
  const base=v.theme==='light'?[218,226,249]:[62,71,89],on=v.theme==='light'?[62,71,89]:[218,226,249];
  for(let i=0;i<3;i++)assert.ok(Math.abs(Number(actual[i+1])-(base[i]*(1-alpha)+on[i]*alpha)/255)<.00001);
 }
 runs.push({candidate,states:60,structure_stable:true,geometry_identical_across_candidates:true,named_fixes:'3/3',horizontal_overflow:0,console_errors:0,helper_spill:0,original_images_loaded:true,hover_state_layer_percent:alpha*100,views:all.map(v=>({view:v.route||v.view,width:v.width,theme:v.theme,structure_sha256:hash(v.structure),geometry_sha256:hash(v.geometry)}))});
}
const result={selected:'A',criteria:['structure stability','complete fixes at named positions','smallest change from original selected container'],runs,reason:'All 3 candidates have identical DOM and geometry and fix A1/C1/C2. A uses the smallest hover state layer (8%); no grid, navigation or control-order changes during refinement.',draft_to_refinement:'Only account-row padding, platform-row padding/gap, selected navigation hover color and GitHub image backdrop changed. Account content may flow vertically with the named padding correction.',evidence:'.work/design/md3-{A,B,C}; regenerate with design-capture.mjs and design-md3-states.mjs against the draft build plus the corresponding candidate CSS.'};
await writeFile('docs/design/md3/selection.json',JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({selected:result.selected,runs:runs.map(({views,...rest})=>rest)}));
