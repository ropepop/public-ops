import { html, reactive } from '@arrow-js/core';

const key = 'ticket.language';
const supported = ['lv', 'en', 'ru'];
let saved;
try { saved = localStorage.getItem(key); } catch {}
export const locale = reactive({ language: supported.includes(saved) ? saved : 'lv' });
export function setLanguage(language) {
  if (!supported.includes(language)) return;
  locale.language = language;
  document.documentElement.lang = language;
  try { localStorage.setItem(key, language); } catch {}
  document.dispatchEvent(new Event('ticket:language'));
}

// Latvian is the existing source copy; keep one shared dictionary for the viewer.
const translations = {
  'Valoda': ['Language', 'Язык'],
  'Reģistrēšanās vilcienā': ['Train check-in', 'Отметка в поезде'],
  'Pēdējās 40 minūtes': ['Last 40 minutes', 'Последние 40 минут'],
  'Uz Rīgu': ['Towards Riga', 'В Ригу'],
  'No Rīgas': ['Away from Riga', 'Из Риги'],
  'Reģistrēties vilcienā': ['Check in', 'Отметиться в поезде'],
  'Mainīt / atjaunot reģistrēšanos': ['Change or renew', 'Изменить или продлить'],
  'Izrakstīties': ['Check out', 'Завершить отметку'],
  'Skatīt biļeti': ['View ticket', 'Посмотреть билет'],
  'Aizvērt': ['Close', 'Закрыть'],
  'Atpakaļ': ['Back', 'Назад'],
  'Braukšanas virziens': ['Direction of travel', 'Направление движения'],
  'Izvēlies vagonu no braukšanas priekšgala': ['Choose your carriage, counted from the front', 'Выберите вагон, считая от начала по ходу движения'],
  'Rīga': ['Riga', 'Рига'],
  'Prom no Rīgas': ['Away from Riga', 'От Риги'],
  'Sākuma punkts': ['Starting point', 'Начальная станция'],
  'Priekšgals': ['Front', 'Начало'],
  'Aizmugure': ['Rear', 'Конец'],
  '{n}. no priekšgala': ['{n} from the front', '{n}-й от начала'],
  '{n}. vagons': ['Carriage {n}', 'Вагон {n}'],
  'Atlikušas {n} min.': ['{n} min left', 'Осталось {n} мин.'],
  'Aktīvas reģistrēšanās: {n}': ['Active check-ins: {n}', 'Активных отметок: {n}'],
  'Pēdējā reģistrēšanās': ['Latest check-in', 'Последняя отметка'],
  'Vēl nav aktīvu reģistrēšanos.': ['No active check-ins yet.', 'Активных отметок пока нет.'],
  'Brīvprātīgas reģistrēšanās uz 40 minūtēm. Tas nav pasažieru skaits un nereģistrē ViVi biļeti.': ['Voluntary check-ins for 40 minutes. This is not a passenger count and does not register your ViVi ticket.', 'Добровольные отметки на 40 минут. Это не число пассажиров и не регистрация билета ViVi.'],
  'Savienojums atjaunojas. Dati var būt novecojuši.': ['Reconnecting. These counts may be out of date.', 'Соединение восстанавливается. Данные могут быть устаревшими.'],
  'Saglabā…': ['Saving…', 'Сохранение…'],
  'Apstiprināt reģistrēšanos': ['Confirm check-in', 'Подтвердить отметку'],
  'Reģistrēšanās saglabāta uz 40 minūtēm.': ["You’re checked in for 40 minutes.", 'Вы отметились на 40 минут.'],
  'Izrakstīšanās saglabāta.': ['You’re checked out.', 'Отметка завершена.'],
  'Reģistrēšanās mainīta citā ierīcē. Atver izvēli vēlreiz.': ['Your check-in changed on another device. Open the selection again.', 'Отметка изменена на другом устройстве. Откройте выбор заново.'],
  'Neizdevās apstiprināt saglabāšanu. Pārbaudi savienojumu un mēģini vēlreiz.': ['Could not confirm saving. Check your connection and try again.', 'Не удалось подтвердить сохранение. Проверьте соединение и повторите попытку.'],
  'Neizdevās apstiprināt izrakstīšanos. Pārbaudi savienojumu un mēģini vēlreiz.': ['Could not confirm check-out. Check your connection and try again.', 'Не удалось подтвердить завершение отметки. Проверьте соединение и повторите попытку.'],
  'Savienojas': ['Connecting', 'Подключение'],
  'Savienots': ['Connected', 'Подключено'],
  'Sākt': ['Start', 'Начать'],
  'Pierakstīties': ['Sign in', 'Войти'],
  'Kods gatavs': ['Code ready', 'Код готов'],
  'Aizvērt kontroles kodu': ['Close inspection code', 'Закрыть код проверки'],
  'Biļetes darbības': ['Ticket actions', 'Действия с билетом'],
  'Pieprasīt kontroles kodu': ['Request inspection code', 'Запросить код проверки'],
  'Reģistrēt atvērto biļeti': ['Register the open ticket', 'Зарегистрировать открытый билет'],
  'Atvērt jaunāko biļeti un reģistrēt': ['Open and register the latest ticket', 'Открыть и зарегистрировать последний билет'],
  'Atvērt jaunāko nereģistrēto biļeti': ['Open the latest unregistered ticket', 'Открыть последний незарегистрированный билет'],
  'Skatīt pēdējo reģistrēto biļeti': ['View the last registered ticket', 'Посмотреть последний зарегистрированный билет'],
  'Atgriezties pie nereģistrētās biļetes': ['Return to the unregistered ticket', 'Вернуться к незарегистрированному билету'],
  'Pieejams pēc jaunākās nereģistrētās biļetes noteikšanas.': ['Available after the latest unregistered ticket is identified.', 'Доступно после определения последнего незарегистрированного билета.'],
  'Pieejams, kad tālrunis ir brīvs.': ['Available when the phone is free.', 'Доступно, когда телефон свободен.'],
  'Ierobežojumi': ['Usage limits', 'Лимиты использования'],
  'Lietošanas ierobežojumi': ['Usage limits', 'Лимиты использования'],
  'Ielādē…': ['Loading…', 'Загрузка…'],
  'Biļešu reģistrācijas': ['Ticket registrations', 'Регистрации билетов'],
  'Kontroles kodi': ['Inspection codes', 'Коды проверки'],
  'Gaida SpaceTime stāvokli.': ['Waiting for an update.', 'Ожидание обновления.'],
  'Kontroles koda cipari': ['Inspection code digits', 'Цифры кода проверки'],
  'Izveidot kodu': ['Create code', 'Создать код'],
  'Reģistrēt atvērto biļeti. Velc pa labi vai nospied Enter vai atstarpi.': ['Register the open ticket. Swipe right or press Enter or Space.', 'Зарегистрировать открытый билет. Проведите вправо или нажмите Enter или пробел.'],
  'ViVi biļetes straume': ['Live ViVi ticket', 'Прямая трансляция билета ViVi'],
  'HDR biļetes priekšskatījums': ['HDR ticket preview', 'Предпросмотр билета HDR'],
  'HDR skats': ['HDR view', 'Просмотр HDR'],
  'Pārlūka spilgtums': ['Browser brightness', 'Яркость в браузере'],
  'HDR padara biļetes attēlu spilgtāku šajā ekrānā.': ['HDR makes the ticket picture brighter on this screen.', 'HDR делает изображение билета ярче на этом экране.'],
  'Skatītāji': ['Viewers', 'Зрители'],
  '{n} lapā': ['{n} on this page', '{n} на странице'],
  'skatās': ['viewing', 'смотрит'],
  'ViVi tālrunī ir izrakstījies. Īpašniekam jāatjauno pierakstīšanās sadaļā Admin.': ['ViVi is signed out on the phone. The owner needs to sign in again from Admin.', 'На телефоне выполнен выход из ViVi. Владельцу нужно восстановить вход в разделе Admin.'],
  'ViVi tālrunī pieprasa uzmanību. Īpašniekam jāpārbauda lietotnē redzamais paziņojums.': ['ViVi needs attention on the phone. The owner should check its message.', 'ViVi на телефоне требует внимания. Владельцу нужно проверить сообщение в приложении.'],
  'Gaida tālruņa stāvokli.': ['Waiting for phone status.', 'Ожидание состояния телефона.'],
  'Tiešraide tiek apturēta. Savienojums atjaunosies automātiski.': ['The stream is stopping. It will reconnect automatically.', 'Трансляция останавливается. Соединение восстановится автоматически.'],
  'Darbība pašlaik nav pieejama.': ['This action is currently unavailable.', 'Сейчас это действие недоступно.'],
  'Gaida stāvokli': ['Waiting for status', 'Ожидание состояния'],
  'Gaida savienojumu.': ['Waiting for a connection.', 'Ожидание соединения.'],
  'Neierobežots režīms': ['Unlimited mode', 'Без ограничений'],
  'Parastie limiti': ['Standard limits', 'Обычные лимиты'],
  '{count} / {limit} pēdējās 60 minūtēs': ['{count} / {limit} in the last 60 minutes', '{count} / {limit} за последние 60 минут'],
  '{count} / {limit} pēdējās {seconds} sekundēs': ['{count} / {limit} in the last {seconds} seconds', '{count} / {limit} за последние {seconds} секунд'],
  'Admina darbības tiek auditētas, bet kvotu nepatērē.': ['Admin actions are recorded but do not use the quota.', 'Действия администратора записываются, но не расходуют квоту.'],
  'Pieejams tagad; starp reģistrācijām jābūt vismaz 30 sekundēm.': ['Available now; allow at least 30 seconds between registrations.', 'Доступно сейчас; между регистрациями должно пройти не менее 30 секунд.'],
  'Limits sasniegts. Pieejams {time}.': ['Limit reached. Available {time}.', 'Лимит достигнут. Доступно {time}.'],
  'Kontroles kodu kvota šim admina kontam netiek piemērota.': ['The inspection code quota does not apply to this admin account.', 'Квота кодов проверки не применяется к этому аккаунту администратора.'],
  'Pieejams tagad.': ['Available now.', 'Доступно сейчас.'],
  'gaida SpaceTime atjauninājumu': ['after the next update', 'после следующего обновления'],
  'pēc {n} s': ['in {n} s', 'через {n} с'],
  'pēc {n} min': ['in {n} min', 'через {n} мин'],
  'pēc {n} min {s} s': ['in {n} min {s} s', 'через {n} мин {s} с'],
  'HDR šajā pārlūkā vai ekrānā nav pieejams. Attēls tiek rādīts SDR režīmā.': ['HDR is unavailable on this browser or screen. Showing SDR.', 'HDR недоступен в этом браузере или на экране. Используется SDR.'],
  'HDR neizdevās. Attēls tiek rādīts SDR režīmā. Lai mēģinātu vēlreiz, izslēdziet un ieslēdziet HDR.': ['HDR failed. Showing SDR. Turn HDR off and on to try again.', 'Ошибка HDR. Используется SDR. Чтобы повторить попытку, выключите и включите HDR.'],
  'Nav nesen reģistrētas biļetes, uz kuru pārslēgties.': ['No recently registered ticket is available.', 'Нет недавно зарегистрированного билета для переключения.'],
  'Biļete ir atvērta un vizuāli apstiprināta.': ['The ticket is open and visually confirmed.', 'Билет открыт и визуально подтверждён.'],
  'Darbība netika pabeigta. Pārbaudiet tālruni.': ['The action did not finish. Check the phone.', 'Действие не завершено. Проверьте телефон.'],
  'Tālrunis izpilda darbību…': ['The phone is carrying out the action…', 'Телефон выполняет действие…'],
  'Gaida pieprasījuma rezultātu…': ['Waiting for the request result…', 'Ожидание результата запроса…'],
  'Tālrunis izpilda iepriekšējo darbību.': ['The phone is finishing the previous action.', 'Телефон выполняет предыдущее действие.'],
  'Atvērtā nereģistrētā biļete ir vizuāli apstiprināta.': ['The open unregistered ticket is visually confirmed.', 'Открытый незарегистрированный билет визуально подтверждён.'],
  'Atvērtā biļete ir reģistrēta un vizuāli apstiprināta.': ['The open ticket is registered and visually confirmed.', 'Открытый билет зарегистрирован и визуально подтверждён.'],
  'Gaida tālruņa apstiprinājumu.': ['Waiting for confirmation from the phone.', 'Ожидание подтверждения от телефона.'],
  'Kontroles kods ir gatavs.': ['The inspection code is ready.', 'Код проверки готов.'],
  'Tālrunis sagatavo kontroles kodu…': ['The phone is preparing the inspection code…', 'Телефон готовит код проверки…'],
  'Kontroles kodu neizdevās parādīt. Pieprasījums nav atkārtots.': ['Could not display the inspection code. The request was not repeated.', 'Не удалось показать код проверки. Запрос не повторялся.'],
  'Darbību savienojums atjaunojas…': ['Reconnecting controls…', 'Соединение с управлением восстанавливается…'],
  'Attēls nav saņemts 30 sekundes. Mēģina vēlreiz…': ['No picture received for 30 seconds. Reconnecting…', 'Изображение не поступало 30 секунд. Повторное подключение…'],
  'Tiešraide rāda biļeti.': ['The live ticket is showing.', 'Билет отображается в прямой трансляции.'],
  'Attēls nav svaigs. Gaida tiešraidi…': ['The picture is out of date. Waiting for the live stream…', 'Изображение устарело. Ожидание прямой трансляции…'],
  'Savienot vēlreiz': ['Reconnect', 'Подключиться снова'],
  'Apturēšanu neizdevās apstiprināt. Īpašniekam jāpārbauda aukstais režīms.': ['Could not confirm stopping. The owner should check sleep mode.', 'Не удалось подтвердить остановку. Владельцу нужно проверить спящий режим.'],
  'Tiešraide tiek pilnībā apturēta. Savienojums atjaunosies automātiski.': ['The stream is shutting down. It will reconnect automatically.', 'Трансляция полностью останавливается. Соединение восстановится автоматически.'],
  'Savienojums pārtrūka. Gaida esošā pieprasījuma rezultātu; tas netiek atkārtots.': ['Connection lost. Waiting for the existing request; it will not be repeated.', 'Соединение прервано. Ожидание результата текущего запроса; он не повторяется.'],
  'Pieprasījums netika pieņemts. Pārbaudiet biļetes stāvokli un ierobežojumus.': ['The request was not accepted. Check the ticket status and limits.', 'Запрос не принят. Проверьте состояние билета и лимиты.'],
  'Iestatījumu neizdevās saglabāt.': ['Could not save the setting.', 'Не удалось сохранить настройку.'],
  'Tālruņa vadība nav pieejama. Vilkšana netika nosūtīta; īpašniekam jāpārbauda tālrunis.': ['Phone control is unavailable. No swipe was sent; the owner should check the phone.', 'Управление телефоном недоступно. Жест не отправлен; владельцу нужно проверить телефон.'],
  'To pašu atvērto biļeti neizdevās apstiprināt; nekas netika pavilkts.': ['Could not confirm the same open ticket; no swipe was sent.', 'Не удалось подтвердить тот же открытый билет; жест не отправлен.'],
  'Pirmā vilkšana biļeti nemainīja. Atkārtotās vilkšanas gatavību nevarēja droši apstiprināt, tāpēc otrā vilkšana netika nosūtīta.': ['The first swipe did not change the ticket. Readiness for another swipe could not be confirmed, so none was sent.', 'Первый жест не изменил билет. Готовность к повторному жесту не подтверждена, поэтому второй жест не отправлен.'],
  'Abi atļautie vilkšanas mēģinājumi tika pabeigti, bet ViVi joprojām rāda nereģistrētu biļeti. Citas vilkšanas netika nosūtītas.': ['Both permitted swipes finished, but ViVi still shows an unregistered ticket. No further swipes were sent.', 'Оба разрешённых жеста выполнены, но ViVi всё ещё показывает незарегистрированный билет. Другие жесты не отправлялись.'],
  'Vilkšanas rezultātu nevarēja droši apstiprināt. Pirms jauna mēģinājuma pārbaudi biļeti vēlreiz.': ['The swipe result could not be confirmed. Check the ticket before trying again.', 'Результат жеста не удалось подтвердить. Проверьте билет перед новой попыткой.'],
  'Pēc pārbaudes reģistrē biļeti vēlreiz citu lietotāju ērtībai.': ['Re-register after inspection for the convenience of other users.', 'После проверки зарегистрируйте билет повторно для удобства других пользователей.']
};

export function t(source, values = {}) {
  const text = locale.language === 'lv' ? source : translations[source]?.[locale.language === 'en' ? 0 : 1] ?? source;
  return text.replace(/\{(\w+)\}/g, (match, name) => values[name] ?? match);
}

export function mountLanguage(mount) {
  document.documentElement.lang = locale.language;
  if (!mount) return;
  html`<select id="viewerLanguage" aria-label="${() => t('Valoda')}" value="${() => locale.language}"
    @change="${event => setLanguage(event.target.value)}"><option value="lv" lang="lv">Latviski</option><option value="en" lang="en">English</option><option value="ru" lang="ru">Русский</option></select>`(mount);
}

export function translatePage(root = document) {
  for (const node of root.querySelectorAll('[data-i18n]')) node.textContent = t(node.dataset.i18n);
  for (const node of root.querySelectorAll('[data-i18n-label]')) node.setAttribute('aria-label', t(node.dataset.i18nLabel));
  for (const node of root.querySelectorAll('#trainCheckinMount, #installTicketMount')) node.lang = locale.language;
}
