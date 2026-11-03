import { chromium } from '/Users/codfrm/Code/agentre/agentre/e2e/node_modules/@playwright/test/index.mjs';
const B='/Users/codfrm/Code/agentre/agentre/frontend/packages/agentre-ui/src/engine/assets/brands/';
const names=['claude.svg','anthropic.svg','openai.svg','codex.svg','pi.svg','openclaw.svg','hermes.png','deepseek.svg','glm.svg','gemini.svg','qwen.svg','kimi.svg','minimax.svg','mistral.svg','xai.svg','meta.svg'];
const b=await chromium.launch(); const p=await b.newPage({viewport:{width:600,height:600}});
for(const n of names){
  const fs=await import('node:fs'); fs.writeFileSync('/tmp/presetgen/brandstk/_r.html',`<html><body style="margin:0;background:transparent"><img id="i" src="file://${B}${n}" style="width:560px;height:560px;object-fit:contain;margin:20px"></body></html>`); await p.goto('file:///tmp/presetgen/brandstk/_r.html');
  await p.waitForTimeout(150);
  await p.locator('#i').screenshot({path:`r-${n.replace(/\.(svg|png)$/,'')}.png`,omitBackground:true});
}
await b.close(); console.log('ok');
