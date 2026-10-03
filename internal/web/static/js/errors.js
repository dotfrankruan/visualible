// Plain-language translation for backend validation problems.
//
// Backend problems are precise and technical by design; this maps the
// common ones into sentences a beginner can act on. Unknown problems
// pass through unchanged rather than being hidden — and the raw text is
// always kept alongside so nothing is lost.

const rules = [
  [/hosts is required/i,
    'Choose which machines this automation runs on (Build page → “runs on”).'],
  [/at least one play is required/i,
    'Add at least one automation step on the Build page.'],
  [/module is required/i,
    'A step is missing what it should do. Select it on the Build page and choose an automation.'],
  [/unknown handler "([^"]+)"/i,
    'A step restarts “$1”, but no such automation exists. Re-add the service step or remove the restart reference.'],
  [/duplicate task id/i,
    'Two automation steps ended up with the same internal identity. Re-add the step that looks wrong.'],
  [/duplicate playbook id|duplicate inventory id/i,
    'This project contains two objects with the same internal identity. Reopen the project; if it persists, export the YAML and import it again.'],
  [/duplicate group name/i,
    'Two machine groups have the same name. Rename one of them on the Targets page.'],
  [/invalid ssh port/i,
    'A machine has an invalid SSH port. Ports must be between 1 and 65535 (Targets page).'],
  [/name is required/i,
    'This automation has no name yet. Give it a name on the Build page.'],
  [/inventory .* has no hosts/i,
    'There are no machines to run on yet. Add them on the Targets page.'],
  [/verbosity must be between/i,
    'The Ansible verbosity setting is out of range (-v through -vvvv).'],
];

export function humanizeProblem(problem) {
  for (const [re, message] of rules) {
    const m = String(problem).match(re);
    if (m) {
      return m[1] ? message.replace('$1', m[1]) : message;
    }
  }
  return problem;
}

// humanizeProblems returns friendly lines, dropping the technical prefix
// ("invalid IR:", "cannot render invalid playbook:") that backends add.
export function humanizeProblems(problems) {
  if (!problems?.length) return '';
  return problems
    .map((p) => humanizeProblem(String(p).replace(/^(invalid IR|cannot render invalid \w+):\s*/i, '')))
    .join('\n');
}
