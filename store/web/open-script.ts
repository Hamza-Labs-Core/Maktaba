// The listing page's one script (#757); no import, so the console's jsdom tests run it too.

/**
 * "Open in my Maktaba" without an installed app (#757): the home's own address, asked here and
 * remembered in this browser only (localStorage; never sent to the store or the cloud), then
 * the home's Settings → Plugins → Browse opened on the plugin. One fixed script for every page
 * (the plugin id is the form's data, never part of the script); the CSP allows exactly it, by
 * its sha256.
 */
export const OPEN_SCRIPT =
  '(function(){var f=document.getElementById("home-open");if(!f)return;var i=document.getElementById("home-addr"),b=document.getElementById("home-bad");' +
  'try{var s=localStorage.getItem("maktaba.home");if(s)i.value=s}catch(e){}' +
  'f.addEventListener("submit",function(e){e.preventDefault();var u=null;try{u=new URL(i.value)}catch(x){}' +
  'if(!u||(u.protocol!=="http:"&&u.protocol!=="https:")){b.hidden=false;return}' +
  'try{localStorage.setItem("maktaba.home",u.origin)}catch(x){}' +
  'location.href=u.origin+"/settings/plugins?store="+encodeURIComponent(f.getAttribute("data-plugin"))})})();';

let scriptHash: Promise<string> | undefined;
/** The CSP source of OPEN_SCRIPT ('sha256-<base64>'). */
export function openScriptHash(): Promise<string> {
  scriptHash ??= crypto.subtle.digest("SHA-256", new TextEncoder().encode(OPEN_SCRIPT)).then(
    (d) => `'sha256-${btoa(String.fromCharCode(...new Uint8Array(d)))}'`,
  );
  return scriptHash;
}
