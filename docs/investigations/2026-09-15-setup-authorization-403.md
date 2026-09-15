# Investigasi: Kegagalan Otorisasi Web Setup Pertama Kali

Tanggal: 2026-09-15
Status: `ROOT CAUSE CONFIRMED` (berdasarkan inspeksi kode; konfirmasi runtime opsional)
Cakupan: setup browser melalui URL TryCloudflare sementara pada instalasi VPS baru.
Catatan: tidak ada perubahan kode dalam investigasi ini. Parsing URL Quick Tunnel
tidak menjadi cakupan dan tidak terlibat.

## Ringkasan Eksekutif

Halaman setup terbuka karena permintaan halaman/GET browser berhasil, tetapi
penulisan terautentikasi pertama dari browser — `POST /v1/bootstrap/verify` —
ditolak sebelum mencapai handler setup oleh middleware CORS global dengan
`403 cors_forbidden`.

Dalam mode setup, allowlist `config.ConsoleCORSOrigins` **kosong** (tidak pernah
diisi oleh `compose.setup.yaml`, `installengine.GenerateConfig`, maupun
`setupinstall.BuildPlan`). Browser mengirim header `Origin` pada
POST/PUT/DELETE same-origin, dan middleware CORS menolak setiap permintaan ke
jalur manajemen yang origin-nya tidak ada di allowlist. Console memetakan
HTTP 403 ke string literal:

```
You do not have permission to view this resource.
```

Itulah tepatnya gejala yang dilaporkan. Kode setup sebenarnya tidak pernah
diterima; panggilan verify tidak pernah mengeksekusi handler-nya.

## Alur Reproduksi

1. `curl bootstrap.sh | sh` → CLI terpasang, layanan setup berjalan, URL Quick
   Tunnel dipublikasikan, browser membuka `https://<acak>.trycloudflare.com/setup`.
2. Halaman tampil; `GET /v1/setup/status` (tanpa `Origin`) berhasil → status tampil.
3. Pengguna memasukkan kode setup; Console menjalankan
   `POST /v1/bootstrap/verify` (JSON, `credentials: "include"`).
4. Permintaan membawa `Origin: https://<acak>.trycloudflare.com`.
5. API membalas
   `403 {"error":{"code":"cors_forbidden","message":"origin is not allowed for this project"}}`.
6. `errorMessage()` Console melihat status 403 → menampilkan pesan permission.
   Wizard tidak bisa lanjut.

## Alur Otorisasi yang Diharapkan

```
POST /v1/bootstrap/verify         (Origin: https://<tunnel>, cookie jar kosong)
  -> CORS mengizinkan origin setup yang sama
  -> handler memvalidasi kode sekali pakai
  -> mengklaim setup state (SetupSessionID/CodeHash)
  -> Set-Cookie: stealth_setup=<enc>; Path=/; HttpOnly; Secure; SameSite=Lax
GET /v1/setup/preflight           (cookie dikirim, GET tidak punya Origin)
  -> requireSetup: dekripsi cookie, cocokkan setup state, verifikasi bootstrap code
  -> 200 OK
PUT /v1/setup/config              (Origin: https://<tunnel>, X-Stealth-Setup: 1)
  -> requireSetupMutation: cek origin CSRF vs externalOrigin() (https via tunnel terdaftar)
  -> 200 OK
```

## Jalur Kode Aktual

- Rantai middleware global (aktif di mode setup): `internal/httpapi/routes.go:19`
  — `r.Use(..., s.cors)`; rute setup terdaftar di `routes.go:24-29`.
- Middleware CORS: `internal/httpapi/server_cors.go:19-84`.
  - `server_cors.go:21` membaca `Origin`; origin kosong langsung lolos (`:22-25`).
  - `server_cors.go:37` memeriksa `s.config.ConsoleCORSOrigins`.
  - `server_cors.go:50-57` — untuk jalur manajemen non-`/v1/projects/{uuid}`,
    origin tak terdaftar → `corsDenied`.
  - `server_cors.go:131-137` — `corsDenied` menulis `403 cors_forbidden`.
- Sumber allowlist: `internal/config/auth.go:57` —
  `parseConsoleCORSOrigins(os.Getenv("CONSOLE_CORS_ORIGINS"))`;
  `internal/config/config.go:421-441`. Hanya di-assign di `auth.go:123`.
  Tidak ada default yang diturunkan dari `PUBLIC_APP_URL`.
- Konfigurasi stack setup: `compose.setup.yaml:51-72` (env `setup`) tidak
  memiliki `CONSOLE_CORS_ORIGINS`; `compose.setup.yaml:112-130` (`setup-proxy`)
  me-mount `console/deploy/nginx.conf`.
- Config hasil generate tidak menyertakannya: `internal/installengine/config.go:104-140`
  dan `internal/setupinstall/plan.go:35-46`.
- Referensi produksi: `compose.production.yaml:17`
  `CONSOLE_CORS_ORIGINS: "${CONSOLE_CORS_ORIGINS:-}"`; `.env.production.example:42`
  kosong.
- Handler verify (tidak pernah tercapai saat origin ada):
  `internal/httpapi/routes_bootstrap.go:8` → `server_bootstrap.go:117-189`.
  Hanya mengembalikan 401/409/5xx — tidak ada jalur 403.
- Permintaan Console: `console/src/api/mutations/auth.ts:34-37`
  (`POST /v1/bootstrap/verify`), client di `console/src/api/client.ts:6-11`
  (`baseUrl` kosong → same-origin, `credentials: "include"`). Alur di
  `console/src/features/auth/browser-setup-flow.ts:260-281`.
- Pemetaan pesan 403: `console/src/components/feedback/error-state.tsx:10-12`.
- Proxy meneruskan `Origin` dan hanya menambahkan header forwarding:
  `console/deploy/nginx.conf:56-73`.
- Cookie (benar, bukan penyebab): `internal/httpapi/setup_support.go:104-135` —
  `stealth_setup`, Path `/`, HttpOnly, `Secure:true`, SameSite Lax, MaxAge≈15m;
  tanpa Domain.
- CSRF (benar, bukan penyebab): `setup_support.go:197-224`
  (`X-Stealth-Setup` + Origin vs `externalOrigin`); `externalOrigin` memaksa
  `https` saat URL quick tunnel terdaftar cocok dengan Host
  (`setup_support.go:271-297`).

## Temuan

### Terkonfirmasi

- Di mode setup `ConsoleCORSOrigins` kosong: tidak diset `compose.setup.yaml`,
  tidak ditulis `installengine`/`setupinstall`, tidak ada default.
- Satu-satunya HTTP 403 pada `POST /v1/bootstrap/verify` adalah penolakan CORS
  global (`server_cors.go:50-57,131-137`). Handler verify sendiri tidak mungkin
  menghasilkan 403.
- Console memetakan semua 403 ke pesan persis yang diamati
  (`error-state.tsx:10-12`).
- Tes setup tidak pernah mengirim `Origin` sehingga lolos:
  `internal/httpapi/setup_routes_test.go:191-202` (request tanpa `Origin`;
  `ConsoleCORSOrigins` tidak diset di `:163-174`). E2E Console men-mock semua
  `/v1/**` (`console/tests/e2e/*.spec.ts`), jadi tidak ada lapisan CI yang
  menguji middleware CORS dengan origin browser.
- `server_cors.go:15-16` mengklaim "Console bridge intentionally strips Origin";
  bridge semacam itu tidak ada di repo ini (`console/next.config.ts` tanpa
  rewrites/middleware; tidak ada handler `app/api`). Asumsi itu kedaluwarsa.

### Keyakinan tinggi

- Browser mengirim `Origin` pada fetch same-origin non-GET/HEAD; karena itu POST
  verify ditolak sebelum validasi kode. GET (`GET /v1/setup/status`, muat
  halaman) berhasil karena GET same-origin tidak membawa `Origin`, sehingga
  halaman tampil normal.

### Mungkin

- Permintaan yang gagal bisa jadi penulisan berikutnya (`PUT /v1/setup/config`)
  alih-alih verify, jika `Origin` absen pada permintaan pertama; mekanisme dan
  perbaikannya tetap sama. Hanya DevTools/curl yang bisa memastikan permintaan
  persisnya.
- `COOKIE_SECURE: "false"` di `compose.setup.yaml:68` bertentangan dengan
  `Secure:true` yang di-hardcode di `setSetupCookie` — inkonsistensi sekunder,
  bukan penyebab 403. Bisa berdampak pada fallback `http://localhost:8081` di
  browser yang menolak cookie Secure lewat HTTP polos.

### Dikesampingkan

- Deteksi/parsing URL Quick Tunnel (URL ditemukan; halaman tampil).
- Nama/domain/path/SameSite/HttpOnly/Secure/kedaluwarsa cookie (benar untuk
  tunnel HTTPS).
- CSRF: penulisan setup mengirim `X-Stealth-Setup: 1` dan `externalOrigin()`
  menghasilkan `https` dari URL quick tunnel yang terdaftar di server, jadi CSRF
  akan lolos. CSRF juga tidak diterapkan pada `/v1/bootstrap/verify`.
- `PUBLIC_APP_URL=http://localhost:8081` vs `https://<tunnel>`: tidak dipakai
  untuk CORS atau cakupan cookie; dipakai untuk email/tautan auth. Tidak
  menghasilkan 403.
- `TRUSTED_PROXY_CIDRS` / `X-Forwarded-Proto`: default `172.30.0.0/24` cocok
  dengan network setup (`compose.setup.yaml:69,145`); skema juga dipaksa oleh
  URL tunnel terdaftar. Bukan penyebab.
- Cloudflare Named Tunnel produksi, onboarding GitHub, pembuatan kode setup —
  tidak tersentuh jalur ini.

## Akar Masalah

Akar masalah terkonfirmasi (berdasarkan inspeksi kode). Deployment mode setup
tidak pernah mengonfigurasi `ConsoleCORSOrigins`, sehingga middleware CORS
deny-by-default di `internal/httpapi/server_cors.go:19-84` menolak penulisan
terautentikasi same-origin dari browser (pertama: `POST /v1/bootstrap/verify`)
dengan `403 cors_forbidden`, karena origin `*.trycloudflare.com` yang acak (dan
fallback `http://localhost:8081`) tidak bisa dikonfigurasi sebelumnya. Console
merender 403 sebagai pesan permission. Kode setup tidak pernah dievaluasi.

Konfirmasi runtime residual (tidak wajib untuk menetapkan penyebab, tetapi untuk
memastikan permintaan persisnya): entri DevTools dari panggilan yang gagal yang
menunjukkan `403` dan body code `cors_forbidden` dengan header `Origin` terisi.

## Dampak Keamanan

- Hanya memblokir setup (ketersediaan). Sifatnya deny-by-default, jadi tidak bisa
  dieksploitasi; ia mencegah penulisan setup yang sah.
- Tidak ada bypass autentikasi. Tidak ada otorisasi rute, atribut cookie, atau
  pemeriksaan CSRF yang dilemahkan.
- Tidak ada kebocoran sesi. `stealth_setup` tetap HttpOnly/Secure/SameSite=Lax,
  host-only.
- Auth produksi tidak terpengaruh oleh bug ini, tetapi ada jebakan yang sama:
  deployment apa pun (termasuk produksi) yang membiarkan `CONSOLE_CORS_ORIGINS`
  kosong akan menolak penulisan browser ke jalur manajemen. Itu persyaratan
  konfigurasi, bukan regresi keamanan.
- Terbatas pada mode setup (plus deployment salah konfigurasi dengan allowlist
  kosong). Tidak ada bukti keterlibatan Cloudflare; tidak ada jalur OAuth yang
  terlibat.

## Pemeriksaan Runtime (aman; tanpa mencetak rahasia)

Set variabel compose yang dipakai proyek setup, mis.
`ENV=/root/.stealth/config.env`, `ROOT=/root/.stealth`.

```bash
# 1. Status service/health (tanpa rahasia)
docker compose --env-file "$ENV" -f "$ROOT/compose.setup.yaml" -p stealth ps
docker inspect --format '{{.Name}} {{.State.Status}} {{.State.Health.Status}}' \
  $(docker ps -q --filter label=com.docker.compose.project=stealth)

# 2. Env non-rahasia terpilih (JANGAN jalankan `printenv` tanpa argumen)
docker exec $(docker ps -q -f label=com.docker.compose.service=setup) \
  printenv SETUP_MODE TRUSTED_PROXY_CIDRS PUBLIC_APP_URL COOKIE_SECURE CONSOLE_CORS_ORIGINS

# 3. Pastikan 403 adalah CORS, tanpa memakai kode sekali pakai yang asli.
#    Ada Origin -> ditolak CORS sebelum handler:
curl -sS -D - -o /tmp/o1 -X POST "https://<tunnel-host>/v1/bootstrap/verify" \
  -H 'Origin: https://<tunnel-host>' -H 'Content-Type: application/json' \
  --data '{"setup_code":"STEALTH-0000-0000-0000"}'
#    Harapan: HTTP 403, body {"error":{"code":"cors_forbidden",...}}
#    Tanpa Origin -> mencapai handler (kode invalid), membuktikan Origin sebagai pembeda:
curl -sS -D - -o /tmp/o2 -X POST "https://<tunnel-host>/v1/bootstrap/verify" \
  -H 'Content-Type: application/json' --data '{"setup_code":"STEALTH-0000-0000-0000"}'
#    Harapan: HTTP 401 invalid_bootstrap_code (atau 429 jika kena rate limit).
#    Gunakan <= 2 panggilan (limit 10/menit).

# 4. Log terfilter (hindari membuang body; tidak ada rahasia yang diharapkan)
docker logs --tail=200 $(docker ps -q -f label=com.docker.compose.service=setup) 2>&1 \
  | grep -E 'cors_forbidden|/v1/bootstrap/verify|"status":403' | tail -20
docker logs --tail=200 $(docker ps -q -f label=com.docker.compose.service=setup-proxy) 2>&1 \
  | grep -E 'POST /v1/bootstrap/verify' | tail -20
```

Hindari: `printenv` polos / `docker inspect` `Config.Env` (mencetak
`FUNCTIONS_SECRET_KEY`, `BOOTSTRAP_CLI_KEY`, `POSTGRES_PASSWORD`,
`REDIS_PASSWORD`, `DATABASE_URL`, `REDIS_URL`), `docker compose config` tanpa
`--quiet`, dan apa pun yang membaca `setup-state.enc` atau token provider.

## Pemeriksaan Browser (DevTools)

- Tab Network, aktifkan "preserve log"; masukkan kode setup.
- Temukan `POST https://<host>/v1/bootstrap/verify`:
  - Status `403`; body respons `{"error":{"code":"cors_forbidden",...}}`;
    tidak ada header respons `Access-Control-Allow-Origin`.
  - Header permintaan: `Origin: https://<host>` ada;
    `Content-Type: application/json`.
- Jika verify justru `200` dengan
  `Set-Cookie: stealth_setup=...; HttpOnly; Secure; SameSite=Lax; Path=/`,
  periksa penulisan berikutnya (`PUT /v1/setup/config` atau
  `POST /v1/setup/github/manifest/start`): pastikan `Origin` dan apakah body
  code `cors_forbidden` (CORS) atau `csrf_failed` (CSRF). Jika cookie
  `stealth_setup` tersimpan, pastikan permintaan berikutnya membawanya
  (Application -> Cookies).
- Jangan mengungkap nilai cookie atau kode setup.

## Rekomendasi Perbaikan (belum diimplementasikan)

Perbaikan terkecil yang aman: di `internal/httpapi/server_cors.go`, sebelum
menolak jalur manajemen, izinkan permintaan ketika `s.config.SetupMode` bernilai
true dan `Origin` terparse sama dengan `s.externalOrigin(r)` milik server (yang
sudah memvalidasi `Host` dan menghasilkan `https` dari URL quick tunnel
terdaftar, atau `http` untuk localhost). Tangani `OPTIONS` dengan cara yang sama.
Ini adalah penegakan same-origin sejati yang dibatasi hanya pada mode setup.

Alasan ini mempertahankan semua properti wajib:

- Semantik kode sekali pakai, kedaluwarsa sesi setup, isolasi setup: tidak
  berubah (tetap ditegakkan di `verifyBootstrapCode`/`setupSupport`).
- Cookie HttpOnly/Secure/SameSite: tidak berubah.
- CSRF: tidak berubah — `requireSetupMutation` tetap mewajibkan
  `X-Stealth-Setup: 1` dan `Origin == externalOrigin`.
- Akses setup Quick Tunnel dan fallback localhost: keduanya diizinkan karena
  `externalOrigin` cocok dengan host tunnel terdaftar dan
  `http://localhost:8081`.
- Auth produksi: tidak berubah (perubahan digerbang pada `SetupMode`; origin
  asing tetap ditolak).

Alternatif yang dapat diterima: isi `ConsoleCORSOrigins` saat startup di mode
setup dengan `http://localhost:8081`/`http://127.0.0.1:8081` dan tambahkan
origin tunnel terdaftar secara dinamis. Lebih banyak bagian bergerak;
`externalOrigin` sudah memusatkan nilai tepercaya.

Jangan nonaktifkan pemeriksaan CORS/origin secara global, dan jangan menyalin
origin tunnel ke allowlist statis.

## Tes Regresi yang Dibutuhkan

Tambahkan (memakai harness test full-handler yang ada yang melewati `cors`):

1. `TestSetupCORSAllowsSameOriginVerifyWrite` — server mode setup;
   `POST /v1/bootstrap/verify` dengan `Host=<tunnel>` dan
   `Origin: https://<tunnel>` → bukan 403 (harap 401 untuk kode dummy), dan
   jalur kode valid menghasilkan 200 + `Set-Cookie: stealth_setup`.
2. `TestSetupCORSRejectsForeignOriginWrite` — host sama,
   `Origin: https://evil.example` → 403 `cors_forbidden`.
3. `TestSetupCORSLocalhostFallback` — `Host: localhost:8081`,
   `Origin: http://localhost:8081` → diizinkan.
4. `TestSetupCORSOptionsPreflightSameOrigin` — `OPTIONS /v1/setup/config` dengan
   `Origin` cocok + `Access-Control-Request-Method: PUT` → 204.
5. Rantai otorisasi end-to-end: daftarkan quick tunnel → verify dengan `Origin`
   → pastikan cookie → `GET /v1/setup/preflight` 200 →
   `PUT /v1/setup/config` 200 dengan `Origin` + `X-Stealth-Setup: 1`.
6. Kontrol negatif: preflight tanpa cookie → 401; cookie setup kedaluwarsa →
   401; mutasi dengan `Origin` salah → 403 (CSRF); mode produksi
   (SetupMode=false) origin tak terdaftar → 403.
7. Pertahankan tes allow/deny `internal/httpapi/server_cors_test.go` yang ada
   tetap hijau.

## Dampak Rilis

- P0 untuk alur setup browser instalasi baru. Ini memblokir seluruh jalur
  onboarding utama; langkah CLI/tunnel/status berhasil tetapi wizard tidak bisa
  maju. Ini bukan bypass keamanan, jadi bukan darurat rilis keamanan, tetapi
  memblokir secara fungsional.
- v0.2.4: cacat ini ada di `v0.2.4` yang sudah dirilis dan merupakan kondisi
  yang sudah ada sebelumnya (setup browser tidak pernah mengisi allowlist mode
  setup). Ini tidak bisa memblokir tag yang sudah dipublikasikan secara
  retroaktif; tindakan yang benar adalah rilis patch lanjutan (mis. `v0.2.5`).
  Jika `v0.2.4` belum dipublikasikan, ini harus memblokirnya.
- v0.3.0-rc.1: harus memblokir RC — release candidate tidak boleh mengirim
  setup pertama kali yang rusak. Perbaikan + tes regresi harus masuk sebelum
  promosi.

## Status

```
STATUS: ROOT CAUSE CONFIRMED
```

Bukti berikutnya yang perlu dicatat (untuk jejak audit, bukan untuk menetapkan
penyebab): entri DevTools/curl yang menunjukkan `POST /v1/bootstrap/verify`
mengembalikan `403` dengan body code `cors_forbidden` saat `Origin` ada, dan
non-403 (401 `invalid_bootstrap_code`) untuk permintaan yang sama tanpa `Origin`.
