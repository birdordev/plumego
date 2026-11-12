import { syncModules } from './sync-modules.mjs';
import { syncReleaseMeta } from './sync-release-meta.mjs';
import { syncRoadmap } from './sync-roadmap.mjs';
import { syncRouting } from './sync-routing.mjs';
import { syncTranslationLag } from './check-translation-lag.mjs';

await syncModules();
await syncReleaseMeta();
await syncRoadmap();
await syncRouting();
await syncTranslationLag();
