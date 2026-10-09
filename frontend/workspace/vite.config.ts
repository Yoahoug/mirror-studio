import {createSpaConfig} from '../shared/vite.config.base';

export default createSpaConfig({
  root: __dirname,
  // Output to Django static directory for production deployment
  outDir: '../../static/workspace',
  assetName: 'workspace',
  port: 3000,
});
