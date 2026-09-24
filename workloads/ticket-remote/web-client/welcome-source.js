import { html } from '@arrow-js/core';
import { isInstalledApp, mountInstallGuide } from './install-guide.mjs';
import { locale, setLanguage } from './viewer-language.mjs';
import { invitationCopy, mountInvitationEntry, mountInvitationWelcome } from './invitation-ui.mjs';

const page = document.querySelector('#welcomePage');
const authURL = page.dataset.authUrl;
const acknowledgementKey = 'ticket.welcomeAcknowledged';
const platform = /Android/i.test(navigator.userAgent) ? 'android'
  : /iPhone|iPad|iPod/i.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1) ? 'ios' : '';
let acknowledged = false, saved, invitation = null;
try { acknowledged = localStorage.getItem(acknowledgementKey) === '1'; saved = localStorage.getItem('ticket.language'); } catch {}
try { invitation = JSON.parse(document.querySelector('#ticketInvitationData')?.textContent || 'null'); } catch {}
const remember = () => { try { localStorage.setItem(acknowledgementKey, '1'); } catch {} };
const supported = language => ['lv', 'en', 'ru'].includes(language);
setLanguage(supported(saved) ? saved : (navigator.languages || [navigator.language]).map(language => language.toLowerCase().split('-')[0]).find(supported) || 'en');
const COPY = {
  en: { title: 'Welcome to Ticket.', message: 'Add Ticket to your home screen for one-tap access.', continue: 'Sign in to Ticket', guide: 'See installation steps', language: 'Language' },
  lv: { title: 'Laipni lūgts Ticket.', message: 'Pievieno Ticket sākuma ekrānam, lai atvērtu ar vienu pieskārienu.', continue: 'Pierakstīties Ticket', guide: 'Skatīt instalēšanas soļus', language: 'Valoda' },
  ru: { title: 'Добро пожаловать в Ticket.', message: 'Добавьте Ticket на главный экран, чтобы открывать его одним касанием.', continue: 'Войти в Ticket', guide: 'Посмотреть шаги установки', language: 'Язык' }
};
const entry = page.dataset.invitationEntry === 'true';
const installed = isInstalledApp();
if (!invitation && !entry && !installed && (!platform || acknowledged)) {
  location.replace(authURL);
} else {
  const copy = () => COPY[locale.language];
  const content = document.querySelector('#welcomeContent');
  html`<div class="welcome-language"><select id="viewerLanguage" aria-label="${() => copy().language}" value="${() => locale.language}" @change="${event => setLanguage(event.target.value)}"><option value="lv" lang="lv">Latviski</option><option value="en" lang="en">English</option><option value="ru" lang="ru">Русский</option></select></div><div id="welcomeJourney"></div>`(content);
  const journey = content.querySelector('#welcomeJourney');
  if (entry) mountInvitationEntry(journey, authURL);
  else if (invitation) mountInvitationWelcome(journey, invitation, platform);
  else if (installed) html`<img class="welcome-icon" src="/pwa/icon-192.png" alt="" width="76" height="76"><h1>${() => copy().title}</h1><div class="welcome-actions"><a id="continueToAuth" class="welcome-primary" href="${authURL}">${() => copy().continue}</a><a class="welcome-quiet" href="/?enterInvite=1">${() => invitationCopy().haveInvite}</a></div>`(journey);
  else {
    html`<img class="welcome-icon" src="/pwa/icon-192.png" alt="" width="76" height="76"><h1>${() => copy().title}</h1><p id="welcomeMessage" class="welcome-message">${() => copy().message}</p><div class="welcome-actions"><a id="continueToAuth" class="welcome-primary" href="${authURL}" @click="${event => { event.preventDefault(); remember(); location.replace(authURL); }}">${() => copy().continue}</a><button id="welcomeInstructions" class="welcome-secondary" type="button">${() => copy().guide}</button></div><a class="welcome-invitation-link" href="/?enterInvite=1">${() => invitationCopy().haveInvite}</a><div id="installTicketMount"></div>`(journey);
    mountInstallGuide(journey.querySelector('#installTicketMount'), { initialPlatform: platform, opener: journey.querySelector('#welcomeInstructions'), onOpen: remember, continueURL: authURL });
  }
  document.querySelector('#welcomeFallback').hidden = true;
  content.hidden = false;
  document.documentElement.dataset.ticketWelcomeUi = 'arrow';
}
