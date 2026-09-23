# Installation guide screenshot sources

Retrieved 2026-09-18. Original pixels and labels are unchanged. SVG viewports in
`install-guide.mjs` crop to the relevant controls; the guide links each source.
Examples show third-party app names, explicitly explained in all three languages.
The six guide illustrations also have explicitly allowlisted `/pwa/install-*`
URLs for the signed-out welcome guide. Other `/static/` files remain private.

| Local file (internal/web/static/) | Original image | Publisher page |
| --- | --- | --- |
| install-apple.png | https://webkit.org/wp-content/uploads/Add-to-Home-Screen-dark-NEW.png | https://webkit.org/blog/17333/webkit-features-in-safari-26-0/ |
| install-safari-steps.jpg | https://images.macrumors.com/t/N8_ELtp0cwCrpH7pDupToWxLYZ0=/2500x0/filters:no_upscale()/article-new/2025/08/ios-add-to-home-screen1.jpg?lossy | https://www.macrumors.com/how-to/save-safari-bookmark-web-app-iphone-home-screen/ |
| install-safari-add.jpg | https://images.macrumors.com/t/1U6AT-y3T_vSBw6Z3aFoK2Y1W_Q=/2500x0/filters:no_upscale()/article-new/2025/08/ios-add-to-home-screen2.jpg?lossy | https://www.macrumors.com/how-to/save-safari-bookmark-web-app-iphone-home-screen/ |
| install-firefox.png | https://assets-prod.sumo.prod.webservices.mozgcp.net/media/uploads/gallery/images/2026-07-27-21-15-25-f71579.png | https://support.mozilla.org/en-US/kb/use-web-apps-firefox-android |
| install-chrome-choice.png | https://developer.chrome.com/static/blog/how_chrome_helps_users_install_the_apps_they_value/howchromehelps--j2ockleskzd.png | https://developer.chrome.com/blog/how_chrome_helps_users_install_the_apps_they_value |
| install-chrome-confirm.png | https://developer.chrome.com/static/blog/how_chrome_helps_users_install_the_apps_they_value/howchromehelps--ucigrdtm70a.png | https://developer.chrome.com/blog/how_chrome_helps_users_install_the_apps_they_value |

Chrome wording also checked against https://support.google.com/chrome/answer/9658361?co=GENIE.Platform%3DAndroid .

## Native Alpha guide

Release source checked on 2026-09-22:

- Official download: https://github.com/cylonid/NativeAlphaForAndroid/releases/tag/v1.5.2
- Release asset: `NativeAlpha-extendedGithub-universal-release-v1.5.2.apk`
- Free Google Play edition: https://play.google.com/store/apps/details?id=com.cylonid.nativealpha
- Paid Google Play Plus edition: https://play.google.com/store/apps/details?id=com.cylonid.nativealpha.pro
- Release README: https://github.com/cylonid/NativeAlphaForAndroid/blob/v1.5.2/README.md
- Edition-specific settings: https://github.com/cylonid/NativeAlphaForAndroid/blob/v1.5.2/app/src/main/java/com/cylonid/nativealpha/WebAppSettingsActivity.kt
- Settings controls: https://github.com/cylonid/NativeAlphaForAndroid/blob/v1.5.2/app/src/main/res/layout/webapp_settings.xml
- English labels: https://github.com/cylonid/NativeAlphaForAndroid/blob/v1.5.2/app/src/main/res/values/strings.xml

The GitHub extendedGithub release includes the paid edition's features for free.
It and Play Store Plus include the Kiosk Mode section; the free Store edition
hides that section. Its switch is labelled “Full Screen (Immersive Mode)”. All
editions open websites without the browser address bar. Editable “Start URL” is
under “Show expert settings”. The guide links the public release with a browser
fallback for GitHub app downloads; it does not require a GitHub account or app.
No APK or third-party source checkout is bundled or hosted, and no local Store
price or ad-free guarantee is claimed.

These source checks do not prove Ticket sign-in, repeated launches or playback
in either installed Native Alpha edition. Independent Android viewer acceptance
remains open. The guide itself has been deployed previously; see `../CURRENT.md`
for release history. Store alternatives are not marked as device-tested.
