// ProfileForm renders the General tab of the settings prototype.
// Fixture for internal/coderefs: the sample session's references point here.
import { useState } from 'react';
import { saveProfile } from './saveProfile';

export function ProfileForm() {
  const [name, setName] = useState('');
  // line 8
  // line 9
  // line 10
  // line 11
  // line 12
  // line 13
  // line 14
  // line 15
  // line 16
  // line 17
  // line 18
  // line 19
  // line 20
  // line 21
  // line 22
  // line 23
  // line 24
  // line 25
  // line 26
  // line 27
  // line 28
  // line 29
  // line 30
  // line 31
  // line 32
  // line 33
  // line 34
  // line 35
  // line 36
  // line 37
  // line 38
  // line 39
  return (
    <form>
      <label>
        Display name
        <input data-testid="display-name" value={name} onChange={e => setName(e.target.value)} />
      </label>
      <button data-testid="save-btn" onClick={() => saveProfile(name)}>
        Save
      </button>
    </form>
  );
}
