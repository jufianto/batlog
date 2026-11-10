import { Resvg } from '@resvg/resvg-js';
import fs from 'node:fs';
// Each job explicitly states its paths, so a portable rebuild needs no repository.
for (const {input, output, width} of JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))) {
  fs.writeFileSync(output, new Resvg(fs.readFileSync(input), {
    fitTo: {mode: 'width', value: width},
  }).render().asPng());
}
