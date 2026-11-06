/**
 * Unit spec for the provider helpers of src/lib/models.ts (GLM-PLAN.md §7.1):
 * a provider's models × efforts flatten into the picker's flat list.
 */

import { test as unit, expect as uexpect } from '@playwright/test';
import { providerCombos, type ProviderUI } from '../src/lib/models';

const glm: ProviderUI = {
    id: 'glm',
    label: 'Claude GLM',
    models: [
        { id: 'glm-5.3', label: 'GLM-5.3', efforts: ['low', 'high', 'max'] },
        { id: 'glm-5.3-flash', label: 'GLM-5.3-Flash', efforts: ['low', 'high', 'max'] },
    ],
};

unit.describe('providerCombos', () => {
    unit('flattens models × efforts into the exact picker rows', () => {
        const combos = providerCombos(glm);
        uexpect(combos.map((c) => c.label)).toEqual([
            'GLM-5.3 low',
            'GLM-5.3 high',
            'GLM-5.3 max',
            'GLM-5.3-Flash low',
            'GLM-5.3-Flash high',
            'GLM-5.3-Flash max',
        ]);
        uexpect(combos[0]).toEqual({
            value: 'glm-5.3::low',
            label: 'GLM-5.3 low',
            model: 'glm-5.3',
            effort: 'low',
        });
    });

    unit('empty models or efforts degrade to an empty list', () => {
        uexpect(providerCombos({ id: 'x', label: 'X', models: [] })).toEqual([]);
        uexpect(
            providerCombos({ id: 'x', label: 'X', models: [{ id: 'm', label: 'M', efforts: [] }] }),
        ).toEqual([]);
    });
});
