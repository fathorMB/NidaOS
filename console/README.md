# The web console of NidaOS

The web console is the main interface of NidaOS (decisions D3, D10 and D29). It is a React and Vite application. nida-core keeps the built files and serves them at `https://nidaos.local`. The console uses only the API of nida-core (`docs/api.md`).

## Commands

| Command | Result |
| --- | --- |
| `npm ci` | Install the tools. |
| `npm run build` | Examine the types and build the files into `core/internal/console/dist`. Then build nida-core again. |
| `npm test` | Run the unit tests (Vitest). |
| `npm run dev` | Start the development server. Set `NIDA_SERVER` to the address of a nida-core, for example `http://localhost:8470`. |
| `node dev/mock.mjs 8480` | Start a fake API with sample data and the built console at `http://127.0.0.1:8480`. The password is `mock password`. |
| `npm run e2e` | Run the browser tests (Playwright). See below. |

## The browser tests

The browser tests sign in, open each page, add and remove an app, and stop the live stream. They run against the fake API or against a real NidaOS:

```sh
npx playwright install chromium
node dev/mock.mjs 8480 &
NIDA_URL=http://127.0.0.1:8480 npm run e2e
```

The VM test (`scripts/vm-test.sh`) runs them against the console of the virtual machine, through the port 8443 of QEMU. It sets `NIDA_URL`, `NIDA_RESOLVE`, `NIDA_PASSWORD` and `NIDA_OUT` (the folder for the screenshots).

## The structure

| File | Content |
| --- | --- |
| `src/api.ts` | The client of the API and the types of the JSON bodies. |
| `src/live.tsx` | The live streams of the metrics and the logs (decision D8). One connection for each stream, for all the pages. |
| `src/data.tsx` | The lists that the menu and the overview use. The console reads them again each 4 seconds. |
| `src/ui.tsx` | The shared parts from the mockup `design/console`: tables, meters, the live state, the dialogs. |
| `src/styles.css` | The design tokens with the light and the dark theme. |
| `src/pages/` | One file for each part of the menu. |
