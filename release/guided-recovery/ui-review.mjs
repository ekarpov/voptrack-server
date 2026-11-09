// Review synthetic HTML emitted by TestUIReviewCorpus. Package paths are local
// operator inputs; this script never downloads browser tooling or real secrets.
import { createRequire } from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';
const require=createRequire(path.resolve(process.env.VOPTRACK_BROWSER_PACKAGE,'package.json'));
const {chromium}=require('playwright');
const AxeBuilder=require('@axe-core/playwright').default;
const root=path.resolve(process.argv[2]);
const browser=await chromium.launch({headless:true,executablePath:process.env.VOPTRACK_CHROMIUM});
const results=[];
try{
 for(const profile of [
  {name:'desktop-light',width:1440,height:1000,theme:'light'},
  {name:'desktop-dark',width:1440,height:1000,theme:'dark'},
  {name:'phone-light',width:390,height:844,theme:'light'},
  {name:'phone-dark',width:390,height:844,theme:'dark'},
 ]){
  const context=await browser.newContext({viewport:{width:profile.width,height:profile.height},colorScheme:profile.theme});
  const page=await context.newPage();
  const output=path.join(root,profile.name);await fs.mkdir(output,{recursive:true});
  for(const name of (await fs.readdir(path.join(root,'html'))).filter(n=>n.endsWith('.html')).sort()){
   await page.goto('file://'+path.join(root,'html',name));
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth);
   if(overflow)throw new Error('horizontal overflow '+profile.name+' '+name);
   const accessibility=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21aa']).analyze();
   if(accessibility.violations.length)throw new Error('accessibility failure '+profile.name+' '+name+' '+JSON.stringify(accessibility.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))));
   const controls=await page.locator('a,button,input:not([type=hidden]),select').count();
   const focused=[];
   for(let i=0;i<controls;i++){
    await page.keyboard.press('Tab');
    const value=await page.evaluate(()=>{
     const e=document.activeElement,c=getComputedStyle(e);
     return {tag:e.tagName,name:e.getAttribute('name')||e.textContent.trim(),outline:c.outlineStyle};
    });
    if(value.tag==='BODY'||value.outline==='none')throw new Error('keyboard focus missing '+name);
    focused.push(value);
   }
   if(new Set(focused.map(v=>v.tag+':'+v.name)).size!==controls)throw new Error('keyboard order skipped control '+name);
   await page.screenshot({path:path.join(output,name.replace('.html','.png')),fullPage:true});
   results.push({profile:profile.name,page:name,overflow:false,accessibility_violations:0,keyboard_controls:controls});
  }
  await context.close();
 }
 await fs.writeFile(path.join(root,'review.json'),JSON.stringify({views:results.length,results},null,2)+'\n');
 console.log('Recovery UI: '+results.length+' views; no overflow, accessibility violations or skipped keyboard controls.');
}finally{await browser.close()}
