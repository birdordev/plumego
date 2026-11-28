import { createRequester } from '../../shared/api/requests'
import { sessionSnapshot } from './store'

export const request = createRequester(() => sessionSnapshot()?.profile.csrf)
