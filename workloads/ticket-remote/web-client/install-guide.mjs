import { html, reactive } from '@arrow-js/core';

// Capture once, before the stream and database finish starting. A prompt is
// single-use; absence of this event says nothing about installation status.
const installation = reactive({ available: false, busy: false, installed: false, status: '' });
let pendingPrompt = null;
window.addEventListener('beforeinstallprompt', event => {
  event.preventDefault();
  pendingPrompt = event;
  installation.available = true;
});
window.addEventListener('appinstalled', () => {
  pendingPrompt = null;
  installation.available = false;
  installation.installed = true;
  installation.status = 'installed';
});

async function install() {
  if (!pendingPrompt || installation.busy) return;
  const prompt = pendingPrompt;
  pendingPrompt = null;
  installation.available = false;
  installation.busy = true;
  installation.status = '';
  try {
    const result = await prompt.prompt();
    const choice = result || await prompt.userChoice;
    if (!installation.installed) installation.status = choice?.outcome === 'accepted' ? 'accepted' : 'dismissed';
  } catch {
    installation.status = 'failed';
  } finally {
    installation.busy = false;
  }
}

const COPY = {
  lv: {
    open: 'Pievienot Ticket sākuma ekrānam', header: 'Ticket · Lietotne sākuma ekrānā', close: 'Aizvērt pamācību',
    title: 'Tava biļete. Viena pieskāriena attālumā.', selectedTitle: 'Pievieno Ticket sākuma ekrānam',
    intro: 'Atver Ticket tieši no sākuma ekrāna bez pārlūka adreses joslas. Tiešraidei joprojām nepieciešams internets.',
    question: 'Kādu tālruni un pārlūku tu izmanto?', visual: 'Pamācība ar attēliem', chrome: 'Instalē vai seko norādēm',
    back: '← Mainīt tālruni vai pārlūku', install: 'Instalēt Ticket',
    launch: 'Lai sāktu, atver jauno Ticket ikonu.', prompt: 'Apstiprini instalēšanu pārlūka logā, tad atver jauno Ticket ikonu.',
    manual: 'Ja instalēšanas poga šeit neparādās, izmanto tālāk aprakstīto Chrome izvēlni. Pārlūks nosaka, kad automātiskā instalēšana ir pieejama.',
    statuses: { installed: 'Ticket ir instalēta. Atver Ticket ikonu sākuma ekrānā.', accepted: 'Instalēšana pieprasīta. Kad sākuma ekrānā parādās Ticket ikona, atver to.', dismissed: 'Instalēšana atcelta. Kad būsi gatavs, izmanto pārlūka izvēlni, kā aprakstīts tālāk.', failed: 'Neizdevās atvērt instalēšanas logu. Izmanto tālāk aprakstīto pārlūka izvēlni.' },
    example: 'Attēlos redzami īstu pārlūku piemēri angļu valodā. Lietotnes nosaukums, izvēlnes valoda un izskats tavā tālrunī var atšķirties.',
    source: 'Attēla avots', note: 'Atver no ikonas',
    noteText: 'Atverot vietni parastā pārlūka cilnē, adreses josla paliks redzama. Ticket pieprasa pilnekrāna režīmu, taču tālrunis var joprojām rādīt laiku, akumulatoru vai navigācijas pogas.',
    firefoxNote: 'Ja Firefox piedāvā tikai parastu saīsni vai jaunā ikona atveras ar adreses joslu, atjaunini Firefox un mēģini vēlreiz. Vari arī instalēt ar Chrome, izvēloties Android · Chrome pamācību.',
    login: 'Sākuma ekrāna lietotnē var būt jāpierakstās vēlreiz. Instalēšana nepiešķir piekļuvi kontam bez apstiprinājuma.', done: 'Sapratu',
    ios: [
      ['Atver Ticket pārlūkā Safari', 'Ja izmanto citu pārlūku vai saiti ziņapmaiņas lietotnē, vispirms atver šo pašu Ticket adresi Safari. Ja nepieciešams, pieraksties.'],
      ['Atver kopīgošanas izvēlni', 'Pieskaries kopīgošanas ikonai (kvadrāts ar bultiņu uz augšu). Kompaktajā skatā vispirms atver ••• izvēlni un izvēlies “Share” (Kopīgot).', 'share'],
      ['Pievieno sākuma ekrānam', 'Ritini kopīgošanas izvēlni un izvēlies “Add to Home Screen” (Pievienot sākuma ekrānam). Ja šīs iespējas nav, pārbaudi “Edit Actions” izvēlnes apakšā.', 'safariAdd'],
      ['Atstāj ieslēgtu “Open as Web App”', 'Ja redzi slēdzi “Open as Web App” (Atvērt kā tīmekļa lietotni), atstāj to ieslēgtu. Saglabā nosaukumu Ticket un pieskaries “Add” (Pievienot). Tad atver Ticket ikonu sākuma ekrānā.', 'apple']
    ],
    android: browser => [
      ['Atver Ticket pārlūkā ' + browser, 'Izmanto pašu pārlūku, nevis ziņapmaiņas lietotnē atvērtu saiti. Ja nepieciešams, pieraksties.'],
      ['Atver pārlūka izvēlni', 'Pieskaries trim punktiem blakus adreses joslai. To atrašanās vieta var atšķirties atkarībā no pārlūka izkārtojuma.', browser === 'Firefox' ? 'firefox' : null],
      ['Izvēlies lietotnes instalēšanu', browser === 'Chrome' ? 'Izvēlies “Install and create shortcut” vai “Add to Home screen”, tad “Install”. Dažās versijās redzēsi “Install app”. Izvēlies instalēšanu, nevis “Create shortcut” (Izveidot saīsni).' : 'Izvēlies “Install” (Instalēt) vai “Add app to Home screen” (Pievienot lietotni sākuma ekrānam). Nosaukums ir atkarīgs no Firefox versijas.', browser === 'Chrome' ? 'chromeChoice' : null],
      ['Apstiprini un atver Ticket', 'Apstiprini “Install” (Instalēt) vai “Add” (Pievienot). Ja tiek prasīts, izvēlies “Add automatically” vai novieto ikonu sākuma ekrānā. Atver jauno Ticket ikonu.', browser === 'Chrome' ? 'chromeConfirm' : null]
    ]
  },
  en: {
    open: 'Add Ticket to Home Screen', header: 'Ticket · Home screen app', close: 'Close installation guide',
    title: 'Your ticket. One tap away.', selectedTitle: 'Add Ticket to your home screen',
    intro: 'Open Ticket directly from your home screen, without the browser address bar. The live stream still needs an internet connection.',
    question: 'Which phone and browser are you using?', visual: 'Visual guide', chrome: 'Install or follow the menu steps',
    back: '← Change phone or browser', install: 'Install Ticket', launch: 'Launch the new Ticket icon to get started.',
    prompt: 'Confirm the browser’s install prompt, then open your new Ticket icon.',
    manual: 'If no install button appears here, use Chrome’s menu below. Chrome decides when the automatic prompt is available.',
    statuses: { installed: 'Ticket is installed. Open the Ticket icon on your home screen to use the app.', accepted: 'Installation requested. Once the Ticket icon appears, open it from your home screen.', dismissed: 'Installation was cancelled. You can use the browser menu steps below whenever you are ready.', failed: 'The install prompt could not open. Use the browser menu steps below.' },
    example: 'These are real browser screenshots in English. The example app name, menu language and appearance may differ on your phone.',
    source: 'Screenshot source', note: 'Launch from the icon',
    noteText: 'Opening the website in a normal browser tab still shows the address bar. Ticket requests full screen; your phone may keep the time, battery or navigation indicators visible.',
    firefoxNote: 'If Firefox only offers a normal shortcut or the new icon still opens with an address bar, update Firefox and try again. You can also install with Chrome using the Android · Chrome guide.',
    login: 'You may need to sign in again in the home-screen app. Installation does not give access to an account you have not been approved to use.', done: 'Got it',
    ios: [
      ['Open Ticket in Safari', 'If you are using another browser or an app’s built-in browser, open this same Ticket address in Safari first. Sign in if asked.'],
      ['Open the Share menu', 'Tap Safari’s Share icon (a square with an upward arrow). In the compact layout, tap the ••• page menu first, then Share.', 'share'],
      ['Add to Home Screen', 'Scroll down the Share menu and tap Add to Home Screen. If it is missing, check Edit Actions at the bottom.', 'safariAdd'],
      ['Keep “Open as Web App” on', 'If this switch is shown, leave it on. Keep the name Ticket and tap Add. Then open the Ticket icon from your home screen.', 'apple']
    ],
    android: browser => [
      ['Open Ticket in ' + browser, 'Use the browser itself, rather than the browser inside a messaging app. Sign in if asked.'],
      ['Open the browser menu', 'Tap the three-dot menu beside the address bar. Its position can vary with your browser layout.', browser === 'Firefox' ? 'firefox' : null],
      ['Choose the app installation option', browser === 'Chrome' ? 'Choose Install and create shortcut or Add to Home screen, then Install. Some versions show Install app. Choose installation rather than Create shortcut.' : 'Choose Install or Add app to Home screen. The wording varies by Firefox version.', browser === 'Chrome' ? 'chromeChoice' : null],
      ['Confirm and open Ticket', 'Confirm Install or Add. If asked, choose Add automatically or place the icon on your home screen. Open that new Ticket icon.', browser === 'Chrome' ? 'chromeConfirm' : null]
    ]
  }
};

// Viewports crop unchanged source screenshots; original labels are not recreated.
const SHOTS = {
  share: ['safari-steps.jpg', 2500, 2462, '1665 1390 675 200', 'MacRumors', 'https://www.macrumors.com/how-to/save-safari-bookmark-web-app-iphone-home-screen/'],
  safariAdd: ['safari-add.jpg', 2500, 2462, '120 1160 990 195', 'MacRumors', 'https://www.macrumors.com/how-to/save-safari-bookmark-web-app-iphone-home-screen/'],
  apple: ['apple.png', 1920, 1080, '744 149 432 339', 'Apple / WebKit', 'https://webkit.org/blog/17333/webkit-features-in-safari-26-0/'],
  firefox: ['firefox.png', 1080, 173, '740 0 340 173', 'Mozilla', 'https://support.mozilla.org/en-US/kb/use-web-apps-firefox-android'],
  chromeChoice: ['chrome-choice.png', 738, 1600, '0 1094 738 420', 'Google', 'https://developer.chrome.com/blog/how_chrome_helps_users_install_the_apps_they_value'],
  chromeConfirm: ['chrome-confirm.png', 738, 1600, '18 645 702 371', 'Google', 'https://developer.chrome.com/blog/how_chrome_helps_users_install_the_apps_they_value']
};
function picture(key, title, copy) {
  if (!key) return '';
  const [file, width, height, viewBox, credit, source] = SHOTS[key];
  return html`<figure class="install-picture"><svg viewBox="${viewBox}" role="img" aria-label="${title}"><image href="${'/static/install-' + file}" width="${width}" height="${height}"></image></svg>
    <figcaption>${copy.source}: <a href="${source}" target="_blank" rel="noopener noreferrer">${credit}</a></figcaption></figure>`;
}

export function mountInstallGuide(mount) {
  if (!mount) return;
  const state = reactive({ platform: '', language: 'lv', appMode: false });
  const copy = () => COPY[state.language];
  const displayMode = window.matchMedia('(display-mode: standalone), (display-mode: fullscreen)');
  const updateMode = () => { state.appMode = displayMode.matches || navigator.standalone === true; };
  updateMode();
  displayMode.addEventListener('change', updateMode);
  let dialog, opener;
  const select = platform => {
    state.platform = platform;
    queueMicrotask(() => dialog.querySelector('.install-heading').focus());
  };
  html`<div lang="${() => state.language}"><button id="installTicket" class="install-ticket-button" type="button" hidden="${() => state.appMode || installation.installed}"
      @click="${() => { state.platform = ''; dialog.showModal(); }}">${() => copy().open} <span aria-hidden="true">↗</span></button>
    <dialog id="installTicketDialog" class="install-dialog" aria-labelledby="installTicketTitle" lang="${() => state.language}"
      @close="${() => { if (!opener.hidden) opener.focus({ preventScroll: true }); }}">
      <div class="install-header"><span>${() => copy().header}</span><button class="install-language" type="button" @click="${() => { state.language = state.language === 'lv' ? 'en' : 'lv'; }}">${() => state.language === 'lv' ? 'English' : 'Latviski'}</button><button type="button" aria-label="${() => copy().close}" @click="${() => dialog.close()}">×</button></div>
      <h2 id="installTicketTitle" class="install-heading" tabindex="-1">${() => state.platform ? copy().selectedTitle : copy().title}</h2>
      <p class="install-intro">${() => copy().intro}</p>
      ${() => !state.platform ? html`<p class="install-question">${() => copy().question}</p>
        <div class="install-choices">
          <button type="button" @click="${() => select('ios')}"><strong>iPhone / iPad</strong><span>Safari · ${() => copy().visual}</span></button>
          <button type="button" @click="${() => select('chrome')}"><strong>Android · Chrome</strong><span>${() => copy().chrome}</span></button>
          <button type="button" @click="${() => select('firefox')}"><strong>Android · Firefox</strong><span>${() => copy().visual}</span></button>
        </div>` : html`
        <button class="install-back" type="button" @click="${() => select('')}">${() => copy().back}</button>
        <h3>${() => ({ ios: 'iPhone / iPad · Safari', chrome: 'Android · Chrome', firefox: 'Android · Firefox' })[state.platform]}</h3>
        ${() => state.platform === 'chrome' ? html`<div class="install-chrome">
          <button class="primary" type="button" hidden="${() => !installation.available}" disabled="${() => installation.busy}" @click="${install}">${() => copy().install}</button>
          <p>${() => installation.installed ? copy().launch : installation.available ? copy().prompt : copy().manual}</p>
          <p role="status" aria-live="polite">${() => copy().statuses[installation.status] || ''}</p>
        </div>` : ''}
        <p class="install-example">${() => copy().example}</p>
        <ol class="install-steps">${() => (state.platform === 'ios' ? copy().ios : copy().android(state.platform === 'chrome' ? 'Chrome' : 'Firefox')).map(([title, description, shot], index) => html`<li><div class="install-step-heading"><span>${index + 1}</span><strong>${title}</strong></div><p>${description}</p>${picture(shot, title, copy())}</li>`)}</ol>
        <div class="install-note"><strong>${() => copy().note}</strong><p>${() => copy().noteText}</p>
        ${() => state.platform === 'firefox' ? html`<p>${() => copy().firefoxNote}</p>` : ''}
        <p>${() => copy().login}</p></div>
        <button class="install-done" type="button" @click="${() => dialog.close()}">${() => copy().done}</button>`}
    </dialog></div>`(mount);
  dialog = mount.querySelector('dialog');
  opener = mount.querySelector('#installTicket');
}
