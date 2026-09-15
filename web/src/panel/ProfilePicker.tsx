import { useState } from 'react';
import { PROFILE_NAMES, WEIGHT_KEYS } from '../api/types';
import type { Profile, ProfileName, WeightKey } from '../api/types';
import type { PlanSettings } from '../state/usePlan';

/** Taken from the comments on each built-in profile, internal/safety/profile.go:48. */
const BLURBS: Record<ProfileName, string> = {
  cautious: 'Riding with children. Long detours for physical separation.',
  comfortable: 'Prefers facilities, tolerates quiet streets, avoids bare arterials.',
  confident: 'Will take a bike lane on an arterial to save real time.',
  shortest: 'Distance only — the baseline every safety route is measured against.',
};

/** Bounds enforced by Profile.Validate(), internal/safety/profile.go:102. */
const WEIGHT_MIN = 0;
const WEIGHT_MAX = 10;

const WEIGHT_LABELS: Record<WeightKey, string> = {
  weight_lts: 'Traffic stress',
  weight_crash: 'Crash history',
  weight_grade: 'Hills',
  weight_surface: 'Surface quality',
  weight_light: 'Street lighting',
  weight_turn: 'Difficult turns',
  weight_hazard: 'Reported hazards',
};

/**
 * Every field the backend's Validate() requires, so a seeded custom object is
 * never missing one.
 *
 * This is not paranoia: an older server build omits `weight_hazard` from the
 * profile it echoes, and a custom object built from that would silently route
 * with the hazard term disabled. Refusing to tune is better than tuning
 * something the rider did not ask for.
 */
function isComplete(p: Profile): boolean {
  const numeric: (keyof Profile)[] = [...WEIGHT_KEYS, 'max_lts', 'stress_budget_m', 'max_detour_ratio'];
  return numeric.every((k) => Number.isFinite(p[k] as number));
}

interface Props {
  settings: PlanSettings;
  onChange: (next: PlanSettings) => void;
  /** The profile the server actually used on the last plan. Seeding the
   *  custom object from this rather than from a local copy of the weights
   *  means the tuning constants stay where they belong — in the backend. */
  seed: Profile | null;
}

export function ProfilePicker({ settings, onChange, seed }: Props) {
  const [advanced, setAdvanced] = useState(false);
  const active = settings.custom;
  const tunable = active ?? (seed && isComplete(seed) ? seed : null);

  function pick(name: ProfileName) {
    onChange({ ...settings, profile: name, custom: null });
  }

  function tune(patch: Partial<Profile>) {
    if (!tunable) return;
    // The server echoes a complete Profile, which is exactly what `custom`
    // requires — a partial object fails Validate() and 400s.
    onChange({ ...settings, custom: { ...tunable, name: 'custom', ...patch } });
  }

  return (
    <section className="profile">
      <div className="profile__tabs" role="group" aria-label="Rider profile">
        {PROFILE_NAMES.map((name) => (
          <button
            key={name}
            type="button"
            className={!active && settings.profile === name ? 'is-active' : ''}
            aria-pressed={!active && settings.profile === name}
            onClick={() => pick(name)}
          >
            {name}
          </button>
        ))}
      </div>
      <p className="profile__blurb">
        {active ? 'Custom weights — the named profiles are a starting point, not a limit.' : BLURBS[settings.profile]}
      </p>

      <label className="toggle">
        <input
          type="checkbox"
          checked={settings.night}
          onChange={(e) => onChange({ ...settings, night: e.target.checked })}
        />
        <span>Riding after dark</span>
      </label>

      <button
        type="button"
        className="link"
        aria-expanded={advanced}
        onClick={() => setAdvanced((v) => !v)}
      >
        {advanced ? 'Hide' : 'Show'} advanced tuning
      </button>

      {advanced && (
        <div className="tuning">
          {!tunable ? (
            <p className="hint">
              {seed
                ? 'This server reports an incomplete profile, so tuning is disabled rather than guessing at the missing weights.'
                : 'Plan a route first — the sliders start from the weights the server actually used.'}
            </p>
          ) : (
            <>
              <Slider
                label="Comfort threshold (max LTS)"
                value={tunable.max_lts}
                min={1}
                max={4}
                step={1}
                format={(v) => `LTS ${v}`}
                onChange={(v) => tune({ max_lts: v })}
              />
              <Slider
                label="Stress budget above that threshold"
                value={tunable.stress_budget_m}
                min={0}
                max={5000}
                step={100}
                format={(v) => (v === 0 ? 'none — hard limit' : `${v} m`)}
                onChange={(v) => tune({ stress_budget_m: v })}
              />
              <Slider
                label="Detour tolerance"
                value={tunable.max_detour_ratio}
                min={1}
                max={4}
                step={0.05}
                format={(v) => `${v.toFixed(2)}×`}
                onChange={(v) => tune({ max_detour_ratio: v })}
              />
              <hr />
              {WEIGHT_KEYS.map((key) => (
                <Slider
                  key={key}
                  label={WEIGHT_LABELS[key]}
                  value={tunable[key]}
                  min={WEIGHT_MIN}
                  max={WEIGHT_MAX}
                  step={0.05}
                  format={(v) => (v === 0 ? 'ignored' : `${v.toFixed(2)}×`)}
                  onChange={(v) => tune({ [key]: v } as Partial<Profile>)}
                />
              ))}
              {active && (
                <button type="button" className="link" onClick={() => pick(settings.profile)}>
                  Reset to {settings.profile}
                </button>
              )}
            </>
          )}
        </div>
      )}
    </section>
  );
}

interface SliderProps {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  format: (v: number) => string;
  onChange: (v: number) => void;
}

function Slider({ label, value, min, max, step, format, onChange }: SliderProps) {
  return (
    <label className="slider">
      <span className="slider__label">{label}</span>
      <span className="slider__value">{format(value)}</span>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
    </label>
  );
}
