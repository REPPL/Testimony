// saveProfile persists the display name.
// Fixture for internal/coderefs.
export async function saveProfile(name: string) {
  const body = JSON.stringify({ name });
  // line 5
  // line 6
  // line 7
  // line 8
  // line 9
  // line 10
  // The intentional flaw: nothing on screen changes when this resolves.
  await fetch('/api/profile', { method: 'POST', body });
}
