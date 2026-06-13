DELETE FROM subject_aliases
WHERE alias LIKE 'my %'
   OR alias IN (
       'brother',
       'dad',
       'daughter',
       'father',
       'friend',
       'girlfriend',
       'boyfriend',
       'husband',
       'mom',
       'mother',
       'partner',
       'sister',
       'son',
       'trainer',
       'wife'
   );
