import { html, reactive } from '@arrow-js/core';
import { locale, setLanguage } from './viewer-language.mjs';

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
  if (!pendingPrompt || installation.busy || installation.installed) return;
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
    open: 'Ticket instalēšanas iespējas', header: 'Ticket · Instalēšanas iespējas', close: 'Aizvērt pamācību', language: 'Pamācības valoda',
    intro: 'Pievieno Ticket sākuma ekrānam. Tiešraidei joprojām nepieciešams internets.',
    question: 'Kādu tālruni tu izmanto?', visual: 'Pamācība ar attēliem', chrome: 'Instalē vai seko norādēm',
    back: '← Atpakaļ', install: 'Instalēt Ticket', continueSignIn: 'Turpināt un pierakstīties',
    continueInvitation: 'Reģistrējies, kad esi gatavs', invitationOpen: 'Kopē uzaicinājuma saiti un atver to šajā pārlūkā. Izmēģinājumam konts nav vajadzīgs.',
    invitationNote: 'Atverot jauno ikonu, sāc savu izmēģinājumu. Ja redzi pierakstīšanās lapu, izvēlies “Tev ir uzaicinājums?” un ielīmē uzaicinājuma saiti. Reģistrējies, kad esi gatavs.',
    invitationStep: ['Pievieno ikonu un sāc izmēģinājumu', 'Izveido Ticket sākuma ekrāna ikonu un atver to. Uzaicinājums ļauj sākt izmēģinājumu bez konta; e-pastu vari apstiprināt vēlāk.'],
    androidChoice: 'Izvēlies, kā atvērt Ticket Android tālrunī.', browserChoice: 'Izvēlies pārlūku.',
    browsers: 'Instalēšana pārlūkā', browsersDetail: 'Chrome vai Firefox', browserBenefit: 'Sākuma ekrāna ikona · Chrome vai Firefox',
    viewer: 'Pilnekrāna skatītājs', viewerDetail: 'Native Alpha · Atsevišķa lietotne',
    viewerIntro: 'Native Alpha atver Ticket bez pārlūka adreses joslas. Izvēlies instalēšanas veidu.',
    installed: 'Ticket jau ir atvērta kā lietotne vai tās instalēšana šajā pārlūkā ir apstiprināta. Atkārtota instalēšana nav vajadzīga. Citas iespējas ir pieejamas izvēlnē Atpakaļ.',
    githubTitle: 'GitHub · Pilnekrāns bez maksas', recommended: 'Ieteicams',
    githubBenefit: 'Maksas versijas pilnekrāna iespējas bez maksas — paslēpj arī Android statusa un navigācijas joslas.',
    githubHelp: 'Kā instalēt no GitHub', githubSteps: 'Atver laidienu pārlūkā vai GitHub lietotnē. Sadaļā “Assets” lejupielādē šo failu:',
    githubInstall: 'Atver lejupielādēto failu, pēc pieprasījuma atļauj instalēšanu no šī avota un instalē.',
    githubFallback: 'Ja GitHub lietotnē lejupielāde neizdodas, atver laidienu pārlūkā. GitHub konts vai lietotne nav nepieciešama.',
    download: 'Lejupielādēt no GitHub', playTitle: 'Google Play · Vienkāršāka instalēšana',
    playBenefit: 'Bezmaksas versija paslēpj pārlūka adreses joslu. Lai paslēptu arī Android statusa un navigācijas joslas, izvēlies maksas Native Alpha Plus.',
    playFree: 'Instalēt bezmaksas versiju', playPlus: 'Iegādāties Plus', setup: 'Pēc instalēšanas',
    copy: 'Kopēt Ticket saiti', link: 'Ticket saite',
    copied: 'Saite nokopēta.', copyFailed: 'Neizdevās kopēt automātiski. Atlasi un nokopē saiti laukā.',
    viewerSteps: [
      ['Pievieno Ticket', 'Kopē Ticket saiti. Native Alpha pievieno tīmekļa lietotni, ielīmē saiti un nosauc to Ticket.'],
      ['Pievieno ikonu un pieraksties', 'Izveido Ticket sākuma ekrāna ikonu, atver to un pieraksties ar savu apstiprināto kontu. Pārlūka pierakstīšanās netiek pārņemta.'],
      ['Pilnekrāns · GitHub vai Plus', 'Ticket iestatījumos atver “Kiosk Mode” un ieslēdz “Full Screen (Immersive Mode)”. Bezmaksas Play Store versijā šo soli izlaid.']
    ],
    viewerSettings: 'Atstāj JavaScript un sīkdatnes ieslēgtas, automātisko pārlādi un reklāmu bloķēšanu — izslēgtu. Neatļauj nederīgus drošības sertifikātus.',
    changeLink: 'Mainīt Ticket saiti',
    changeLinkText: 'Native Alpha sarakstā atver Ticket iestatījumus → “Show expert settings” → “Start URL”. Ievadi jauno HTTPS adresi un saglabā.',
    viewerNote: 'Adrese ikdienas skatā',
    viewerNoteText: 'Pilnekrāna skatā nav parastās pārlūka adreses joslas. Adrese joprojām var būt redzama pierakstoties, iestatījumos vai pašas vietnes saturā. Tālruņa sistēmas indikatori var parādīties pēc žesta.',
    viewerLogin: 'Ja pierakstīšanās, tiešraide vai vajadzīgā funkcija nedarbojas, izvēlies instalēšanu pārlūkā. Lietotnes iespējas var atšķirties; HDR un paziņojumi nav garantēti.',
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
    open: 'Ticket installation options', header: 'Ticket · Installation options', close: 'Close installation guide', language: 'Instruction language',
    intro: 'Add Ticket to your home screen. The live picture still needs internet.',
    question: 'Which phone are you using?', visual: 'Visual guide', chrome: 'Install or follow the menu steps',
    back: '← Back', install: 'Install Ticket', continueSignIn: 'Continue to sign-in', launch: 'Launch the new Ticket icon to get started.',
    continueInvitation: 'Register when you’re ready', invitationOpen: 'Copy the invitation link and open it in this browser. You do not need an account to try Ticket.',
    invitationNote: 'Open the new icon to start your trial. If it shows sign-in, choose “Have an invitation?” and paste your invitation link. Register whenever you’re ready.',
    invitationStep: ['Add the icon and start your trial', 'Create a Ticket home-screen shortcut and open it. Your invitation lets you start without an account; you can verify your email later.'],
    androidChoice: 'Choose how to open Ticket on your Android phone.', browserChoice: 'Choose your browser.',
    browsers: 'Browser installation', browsersDetail: 'Chrome or Firefox', browserBenefit: 'Home-screen icon · Chrome or Firefox',
    viewer: 'Full-screen viewer', viewerDetail: 'Native Alpha · Separate app',
    viewerIntro: 'Native Alpha opens Ticket without the browser address bar. Choose how to install it.',
    installed: 'Ticket is already open as an app, or installation in this browser has been confirmed. You do not need to install it again. Use Back to explore other options.',
    githubTitle: 'GitHub · Full screen, free', recommended: 'Recommended',
    githubBenefit: 'Get the paid edition’s full-screen features for free, including hiding Android’s status and navigation bars.',
    githubHelp: 'How to install from GitHub', githubSteps: 'Open the release in your browser or GitHub app. Under “Assets”, download this file:',
    githubInstall: 'Open the downloaded file, allow installation from that source if prompted, and install.',
    githubFallback: 'If downloading in the GitHub app fails, open the release in your browser. No GitHub account or app is needed.',
    download: 'Get from GitHub', playTitle: 'Google Play · Easier installation',
    playBenefit: 'The free version hides the browser address bar. Choose paid Native Alpha Plus to also hide Android’s status and navigation bars.',
    playFree: 'Install free version', playPlus: 'Get Plus — paid', setup: 'After installation',
    copy: 'Copy Ticket link', link: 'Ticket link',
    copied: 'Link copied.', copyFailed: 'Automatic copying failed. Select and copy the address in the field.',
    viewerSteps: [
      ['Add Ticket', 'Copy the Ticket link. Add a web app in Native Alpha, paste the link and name it Ticket.'],
      ['Add the icon and sign in', 'Create a Ticket home-screen shortcut, open it and sign in with your approved account. Your browser login is not carried over.'],
      ['Full screen · GitHub or Plus', 'In Ticket’s settings, open “Kiosk Mode” and enable “Full Screen (Immersive Mode)”. Skip this step in the free Play Store version.']
    ],
    viewerSettings: 'Keep JavaScript and cookies on, automatic refresh and ad blocking off. Do not allow invalid security certificates.',
    changeLink: 'Change the Ticket link',
    changeLinkText: 'In Native Alpha’s list, open Ticket’s settings → “Show expert settings” → “Start URL”. Enter the new HTTPS address and save.',
    viewerNote: 'The address during everyday use',
    viewerNoteText: 'Full-screen viewing has no regular browser address bar. The address may still appear during sign-in, in settings or in the website’s own content. Android system indicators may appear after a gesture.',
    viewerLogin: 'If sign-in, the live picture or a feature you need does not work, choose browser installation. App capabilities can differ; HDR and notifications are not guaranteed.',
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
  },
  ru: {
    open: 'Способы установки Ticket', header: 'Ticket · Способы установки', close: 'Закрыть инструкцию', language: 'Язык инструкции',
    intro: 'Добавьте Ticket на главный экран. Для прямой трансляции по-прежнему нужен интернет.',
    question: 'Каким телефоном вы пользуетесь?', visual: 'Инструкция с изображениями', chrome: 'Установите или следуйте инструкции',
    back: '← Назад', install: 'Установить Ticket', continueSignIn: 'Перейти ко входу', launch: 'Чтобы начать, нажмите на новый значок Ticket.',
    continueInvitation: 'Регистрируйтесь, когда будете готовы', invitationOpen: 'Скопируйте ссылку-приглашение и откройте её в этом браузере. Для пробного доступа аккаунт не нужен.',
    invitationNote: 'Откройте новый значок, чтобы начать пробный просмотр. Если появится вход, выберите «Есть приглашение?» и вставьте ссылку-приглашение. Регистрируйтесь, когда будете готовы.',
    invitationStep: ['Добавьте значок и начните пробный просмотр', 'Создайте значок Ticket на главном экране и откройте его. Приглашение позволяет начать без аккаунта; подтвердить почту можно позже.'],
    androidChoice: 'Выберите, как открывать Ticket на телефоне Android.', browserChoice: 'Выберите браузер.',
    browsers: 'Установка через браузер', browsersDetail: 'Chrome или Firefox', browserBenefit: 'Значок на главном экране · Chrome или Firefox',
    viewer: 'Полноэкранный просмотр', viewerDetail: 'Native Alpha · Отдельное приложение',
    viewerIntro: 'Native Alpha открывает Ticket без адресной строки браузера. Выберите способ установки.',
    installed: 'Ticket уже работает как приложение или установка в этом браузере подтверждена. Повторная установка не нужна. Нажмите «Назад», чтобы посмотреть другие варианты.',
    githubTitle: 'GitHub · Полный экран бесплатно', recommended: 'Рекомендуем',
    githubBenefit: 'Полноэкранные возможности платной версии бесплатно — включая скрытие строки состояния и панели навигации Android.',
    githubHelp: 'Как установить с GitHub', githubSteps: 'Откройте выпуск в браузере или приложении GitHub. В разделе «Assets» скачайте этот файл:',
    githubInstall: 'Откройте скачанный файл, при запросе разрешите установку из этого источника и установите приложение.',
    githubFallback: 'Если скачивание в приложении GitHub не работает, откройте выпуск в браузере. Аккаунт и приложение GitHub не нужны.',
    download: 'Скачать с GitHub', playTitle: 'Google Play · Проще установить',
    playBenefit: 'Бесплатная версия скрывает адресную строку браузера. Чтобы скрыть также строку состояния и панель навигации Android, выберите платную Native Alpha Plus.',
    playFree: 'Установить бесплатно', playPlus: 'Купить Plus', setup: 'После установки',
    copy: 'Скопировать ссылку на Ticket', link: 'Ссылка на Ticket',
    copied: 'Ссылка скопирована.', copyFailed: 'Не удалось скопировать автоматически. Выделите адрес в поле и скопируйте его.',
    viewerSteps: [
      ['Добавьте Ticket', 'Скопируйте ссылку на Ticket. Добавьте веб-приложение в Native Alpha, вставьте ссылку и назовите его Ticket.'],
      ['Добавьте значок и войдите', 'Создайте значок Ticket на главном экране, откройте его и войдите в аккаунт с одобренным доступом. Вход из браузера не переносится.'],
      ['Полный экран · GitHub или Plus', 'В настройках Ticket откройте «Kiosk Mode» и включите «Full Screen (Immersive Mode)». В бесплатной версии из Play Store пропустите этот шаг.']
    ],
    viewerSettings: 'Оставьте JavaScript и файлы cookie включёнными, автообновление страницы и блокировку рекламы — выключенными. Не разрешайте недействительные сертификаты безопасности.',
    changeLink: 'Изменить ссылку на Ticket',
    changeLinkText: 'В списке Native Alpha откройте настройки Ticket → «Show expert settings» → «Start URL». Введите новый HTTPS-адрес и сохраните.',
    viewerNote: 'Адрес при повседневном использовании',
    viewerNoteText: 'В полноэкранном режиме нет обычной адресной строки браузера. Адрес по-прежнему может появляться при входе в аккаунт, в настройках или в содержимом самого сайта. После жеста могут появиться системные индикаторы Android.',
    viewerLogin: 'Если вход в аккаунт, прямая трансляция или нужная функция не работают, выберите установку через браузер. Возможности приложений могут различаться; работа HDR и уведомлений не гарантируется.',
    prompt: 'Подтвердите установку в окне браузера, затем нажмите на новый значок Ticket.',
    manual: 'Если кнопка установки здесь не появилась, воспользуйтесь меню Chrome, как описано ниже. Браузер определяет, когда доступно автоматическое предложение установки.',
    statuses: { installed: 'Ticket установлен. Нажмите на значок Ticket на главном экране.', accepted: 'Запрос на установку отправлен. Когда значок Ticket появится на главном экране, нажмите на него.', dismissed: 'Установка отменена. Когда будете готовы, воспользуйтесь меню браузера, как описано ниже.', failed: 'Не удалось открыть окно установки. Воспользуйтесь меню браузера, как описано ниже.' },
    example: 'На изображениях показаны настоящие браузеры с интерфейсом на английском языке. Название приложения в примере, язык меню и внешний вид на вашем телефоне могут отличаться.',
    source: 'Источник изображения', note: 'Открывайте через значок',
    noteText: 'Если открыть сайт в обычной вкладке браузера, адресная строка останется видимой. Ticket запрашивает полноэкранный режим, но телефон может продолжать показывать время, заряд батареи или кнопки навигации.',
    firefoxNote: 'Если Firefox предлагает только обычный ярлык или новый значок открывает страницу с адресной строкой, обновите Firefox и попробуйте ещё раз. Также можно установить Ticket через Chrome, выбрав инструкцию Android · Chrome.',
    login: 'В приложении с главного экрана может потребоваться повторный вход. Установка не заменяет одобрение доступа к аккаунту.', done: 'Понятно',
    ios: [
      ['Откройте Ticket в Safari', 'Если вы используете другой браузер или браузер внутри приложения, сначала откройте этот же адрес Ticket в Safari. При необходимости войдите в аккаунт.'],
      ['Откройте меню «Поделиться»', 'Нажмите значок «Поделиться» в Safari — квадрат со стрелкой вверх. В компактном режиме сначала откройте меню •••, затем выберите «Share» (Поделиться).', 'share'],
      ['Добавьте на главный экран', 'Прокрутите меню «Поделиться» и выберите «Add to Home Screen» (На экран «Домой»). Если этого пункта нет, проверьте «Edit Actions» (Редактировать действия) внизу меню.', 'safariAdd'],
      ['Оставьте «Open as Web App» включённым', 'Если виден переключатель «Open as Web App» (Открывать как веб-приложение), оставьте его включённым. Сохраните название Ticket и нажмите «Add» (Добавить). Затем нажмите на значок Ticket на главном экране.', 'apple']
    ],
    android: browser => [
      ['Откройте Ticket в ' + browser, 'Используйте сам браузер, а не браузер внутри мессенджера. При необходимости войдите в аккаунт.'],
      ['Откройте меню браузера', 'Нажмите на три точки рядом с адресной строкой. Их расположение зависит от интерфейса браузера.', browser === 'Firefox' ? 'firefox' : null],
      ['Выберите установку приложения', browser === 'Chrome' ? 'Выберите «Install and create shortcut» (Установить и создать ярлык) или «Add to Home screen» (Добавить на главный экран), затем «Install» (Установить). В некоторых версиях пункт называется «Install app» (Установить приложение). Выбирайте установку, а не «Create shortcut» (Создать ярлык).' : 'Выберите «Install» (Установить) или «Add app to Home screen» (Добавить приложение на главный экран). Название зависит от версии Firefox.', browser === 'Chrome' ? 'chromeChoice' : null],
      ['Подтвердите и откройте Ticket', 'Подтвердите «Install» (Установить) или «Add» (Добавить). Если появится запрос, выберите «Add automatically» (Добавить автоматически) или разместите значок на главном экране. Нажмите на новый значок Ticket.', browser === 'Chrome' ? 'chromeConfirm' : null]
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
  return html`<figure class="install-picture"><svg viewBox="${viewBox}" role="img" aria-label="${title}"><image href="${'/pwa/install-' + file}" width="${width}" height="${height}"></image></svg>
    <figcaption>${copy.source}: <a href="${source}" target="_blank" rel="noopener noreferrer">${credit}</a></figcaption></figure>`;
}

export function isInstalledApp() {
  return window.matchMedia('(display-mode: standalone), (display-mode: fullscreen)').matches || navigator.standalone === true;
}

export function mountInstallGuide(mount, { initialPlatform = '', opener: externalOpener = null, onOpen, onClose, continueURL = '', ticketURL: suppliedTicketURL = '', invitation = false } = {}) {
  if (!mount) return;
  const state = reactive({ platform: '', appMode: false, copyStatus: '' });
  const copy = () => COPY[locale.language];
  const parents = { ios: '', android: '', browsers: 'android', native: 'android', chrome: 'browsers', firefox: 'browsers' };
  const ticketURL = suppliedTicketURL || new URL('/', window.location.href).href;
  const alreadyInstalled = () => state.appMode || installation.installed;
  const browserGuide = () => ['ios', 'chrome', 'firefox'].includes(state.platform);
  const title = () => ({ ios: 'iPhone / iPad · Safari', android: 'Android', browsers: copy().browsers,
    native: copy().viewer, chrome: 'Android · Chrome', firefox: 'Android · Firefox' })[state.platform] || copy().question;
  const menuPage = () => ['', 'android', 'browsers'].includes(state.platform);
  const displayMode = window.matchMedia('(display-mode: standalone), (display-mode: fullscreen)');
  const updateMode = () => { state.appMode = isInstalledApp(); };
  updateMode();
  displayMode.addEventListener('change', updateMode);
  let dialog, opener;
  const select = platform => {
    state.platform = platform;
    state.copyStatus = '';
    queueMicrotask(() => { dialog.scrollTop = 0; dialog.querySelector('.install-heading').focus({ preventScroll: true }); });
  };
  const open = () => { onOpen?.(); select(initialPlatform); dialog.showModal(); };
  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(ticketURL);
      state.copyStatus = 'copied';
    } catch {
      state.copyStatus = 'copyFailed';
      const input = dialog.querySelector('#installTicketURL');
      input?.focus();
      input?.select();
    }
  };
  const linkControl = () => html`<label class="install-url-label" for="installTicketURL">${() => copy().link}</label>
    <input id="installTicketURL" class="install-url" type="url" readonly value="${ticketURL}">
    <button class="install-copy" type="button" @click="${copyLink}">${() => copy().copy}</button>
    <p role="status" aria-live="polite">${() => copy()[state.copyStatus] || ''}</p>`;
  html`<div lang="${() => locale.language}">${externalOpener ? '' : html`<button id="installTicket" class="install-ticket-button" type="button"
      @click="${open}">${() => copy().open} <span aria-hidden="true">↗</span></button>`}
    <dialog id="installTicketDialog" class="install-dialog" aria-labelledby="installTicketTitle" lang="${() => locale.language}" data-install-page="${() => state.platform || 'os'}"
      @close="${() => { onClose?.(); opener.focus({ preventScroll: true }); }}">
      <div class="install-header"><span>${() => copy().header}</span><select class="install-language" aria-label="${() => copy().language}" value="${() => locale.language}" @change="${event => { setLanguage(event.target.value); }}"><option value="lv" lang="lv">Latviski</option><option value="en" lang="en">English</option><option value="ru" lang="ru">Русский</option></select><button type="button" aria-label="${() => copy().close}" @click="${() => dialog.close()}">×</button></div>
      <h2 id="installTicketTitle" class="install-heading" tabindex="-1">${title}</h2>
      ${() => state.platform === '' ? html`<p class="install-intro">${() => copy().intro}</p>` : state.platform === 'native' ? html`<p class="install-intro">${() => copy().viewerIntro}</p>` : ''}
      ${() => state.platform ? html`<button class="install-back" type="button" @click="${() => select(parents[state.platform])}">${() => copy().back}</button>` : ''}
      <p class="install-question" hidden="${() => !['android', 'browsers'].includes(state.platform)}">${() => state.platform === 'android' ? copy().androidChoice : copy().browserChoice}</p>
      <div class="install-choices" hidden="${() => state.platform !== ''}">
        <button type="button" @click="${() => select('ios')}"><strong>iPhone / iPad</strong><span>Safari · ${() => copy().visual}</span></button>
        <button type="button" @click="${() => select('android')}"><strong>Android</strong><span>${() => copy().browsersDetail} · Native Alpha</span></button>
      </div>
      <div class="install-choices" hidden="${() => state.platform !== 'android'}">
        <button type="button" @click="${() => select('browsers')}"><strong><span class="install-choice-icon" aria-hidden="true">🌐</span>${() => copy().browsers}</strong><span>${() => copy().browserBenefit}</span></button>
        <button type="button" @click="${() => select('native')}"><strong><span class="install-choice-icon" aria-hidden="true">📱</span>${() => copy().viewer}</strong><span>${() => copy().viewerDetail}</span></button>
      </div>
      <div class="install-choices" hidden="${() => state.platform !== 'browsers'}">
        <button type="button" @click="${() => select('chrome')}"><strong>Chrome</strong><span>${() => copy().chrome}</span></button>
        <button type="button" @click="${() => select('firefox')}"><strong>Firefox</strong><span>${() => copy().visual}</span></button>
      </div>
      ${() => browserGuide() && alreadyInstalled() ? html`<p class="install-note" role="status">${() => copy().installed}</p>` : ''}
      ${() => browserGuide() && !alreadyInstalled() ? html`
        ${() => state.platform === 'chrome' ? html`<div class="install-chrome">
          <button class="primary" type="button" hidden="${() => !installation.available}" disabled="${() => installation.busy}" @click="${install}">${() => copy().install}</button>
          <p>${() => installation.installed ? copy().launch : installation.available ? copy().prompt : copy().manual}</p>
          <p role="status" aria-live="polite">${() => copy().statuses[installation.status] || ''}</p>
        </div>` : ''}
        <p class="install-example">${() => copy().example}</p>
        ${invitation ? linkControl : ''}
        <ol class="install-steps">${() => (state.platform === 'ios' ? copy().ios : copy().android(state.platform === 'chrome' ? 'Chrome' : 'Firefox')).map(([title, description, shot], index) => html`<li><div class="install-step-heading"><span>${index + 1}</span><strong>${title}</strong></div><p>${invitation && index === 0 ? copy().invitationOpen : description}</p>${picture(shot, title, copy())}</li>`.key(state.platform + locale.language + index))}</ol>
        <div class="install-note"><strong>${() => copy().note}</strong><p>${() => copy().noteText}</p>
        ${() => state.platform === 'firefox' ? html`<p>${() => copy().firefoxNote}</p>` : ''}
        <p>${() => invitation ? copy().invitationNote : copy().login}</p></div>` : ''}
      ${() => state.platform === 'native' ? html`
        <section class="install-option" aria-labelledby="installGithubTitle">
          <span class="install-recommended">${() => copy().recommended}</span>
          <h3 id="installGithubTitle">${() => copy().githubTitle}</h3>
          <p>${() => copy().githubBenefit}</p>
          <a class="install-download" href="https://github.com/cylonid/NativeAlphaForAndroid/releases/tag/v1.5.2" target="_blank" rel="noopener noreferrer">${() => copy().download} ↗</a>
          <details><summary>${() => copy().githubHelp}</summary>
            <p>${() => copy().githubSteps} <code class="install-apk">NativeAlpha-extendedGithub-universal-release-v1.5.2.apk</code></p>
            <p>${() => copy().githubInstall}</p><p>${() => copy().githubFallback}</p>
          </details>
        </section>
        <section class="install-option" aria-labelledby="installPlayTitle">
          <h3 id="installPlayTitle">${() => copy().playTitle}</h3><p>${() => copy().playBenefit}</p>
          <div class="install-store-links">
            <a class="install-download" href="https://play.google.com/store/apps/details?id=com.cylonid.nativealpha" target="_blank" rel="noopener noreferrer">${() => copy().playFree} ↗</a>
            <a class="install-download" href="https://play.google.com/store/apps/details?id=com.cylonid.nativealpha.pro" target="_blank" rel="noopener noreferrer">${() => copy().playPlus} ↗</a>
          </div>
        </section>
        <h3>${() => copy().setup}</h3>
        ${linkControl}
        <ol class="install-steps">${() => copy().viewerSteps.map((step, index) => { const [heading, description] = invitation && index === 1 ? copy().invitationStep : step; return html`<li><div class="install-step-heading"><span>${index + 1}</span><strong>${heading}</strong></div><p>${description}</p></li>`; })}</ol>
        <p>${() => copy().viewerSettings}</p>
        <details class="install-link-settings"><summary>${() => copy().changeLink}</summary><p>${() => copy().changeLinkText}</p></details>
        <div class="install-note"><strong>${() => copy().viewerNote}</strong><p>${() => copy().viewerNoteText}</p><p>${() => copy().viewerLogin}</p></div>` : ''}
      ${continueURL ? html`<a id="installContinueAuth" class="install-download install-done" href="${continueURL}">${() => invitation ? copy().continueInvitation : copy().continueSignIn} →</a>` : ''}
      ${() => !menuPage() ? html`<button class="install-done" type="button" @click="${() => dialog.close()}">${() => copy().done}</button>` : ''}
    </dialog></div>`(mount);
  dialog = mount.querySelector('dialog');
  opener = externalOpener || mount.querySelector('#installTicket');
  if (externalOpener) externalOpener.addEventListener('click', open);
}
