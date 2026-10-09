# Report assets

The report implementation and its UI integration are licensed under Apache-2.0
with authorization from their copyright holder. See `LICENSE` and `../../NOTICE`.

`report.js` and `report.css` are prebuilt assets embedded into generated reports.
The bundled flamegraph renderer is the pnpm dependency
`@simonswine/grafana-flamegraph` (Apache-2.0); its
license is preserved in `LICENSE.flamegraph`. Bundled React, React DOM,
Scheduler, tinycolor2, uFuzzy, and fflate licenses are preserved in `licenses/`.

Frontend source is in the parent directory (`../`). Rebuild with `pnpm install --frozen-lockfile && pnpm run build`
from that directory. The viewer fetches native `.pprof` files, decompresses and
decodes them in the browser, and offers baseline/comparison profile downloads.
The local report embeds the same pprof bytes as data URLs, so it works offline
without neighboring files. The web report fetches profiles on demand over HTTP.

Third-party components are not relicensed by the project's Apache-2.0 license.
