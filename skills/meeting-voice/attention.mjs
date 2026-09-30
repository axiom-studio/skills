// Local routing only: names are untrusted display labels, never identities or
// approval authority. No model call is needed for passive meeting speech.
const words = text => String(text).normalize('NFKC').toLocaleLowerCase('und').match(/[\p{L}\p{N}]+/gu) ?? [];

export function isAddressed(text, phrases) {
  const input = words(text);
  return phrases.some(phrase => {
    const target = words(phrase);
    return target.length > 0 && input.some((_, start) =>
      target.every((word, offset) => input[start + offset] === word));
  });
}

// Samples must cover the actual audio window, not the later STT completion.
// Multiple simultaneous speakers or changing labels deliberately stay unknown.
export function attributeSpeaker(samples, start, end) {
  const relevant = samples.filter(sample => sample.at >= start && sample.at <= end);
  if (!relevant.length || relevant.some(sample => sample.speakers.length !== 1)) return 'Unknown speaker';
  const labels = new Set(relevant.map(sample => sample.speakers[0].trim()).filter(Boolean));
  return labels.size === 1 ? [...labels][0].slice(0, 120) : 'Unknown speaker';
}
