<?php
require_once __DIR__ . '/config.php';

// Get PostgreSQL Database Connection
function get_db_connection() {
    static $pdo = null;
    if ($pdo === null) {
        $dsn = sprintf("pgsql:host=%s;port=%s;dbname=%s", DB_HOST, DB_PORT, DB_NAME);
        try {
            $pdo = new PDO($dsn, DB_USER, DB_PASS, [
                PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
                PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
                PDO::ATTR_EMULATE_PREPARES => false,
            ]);
        } catch (PDOException $e) {
            throw new Exception("Database connection failed: " . $e->getMessage());
        }
    }
    return $pdo;
}

// Generate Vector Embedding from Ollama
function get_embedding($text, $type = 'document') {
    $url = EMBEDDING_URL;
    $model = EMBEDDING_MODEL;
    
    // Add appropriate prefix for nomic-embed-text
    $prefix = ($type === 'query') ? EMBEDDING_QUERY_PREFIX : EMBEDDING_DOC_PREFIX;
    $input_text = $prefix . $text;

    $ch = curl_init("$url/api/embed");
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
        'model' => $model,
        'input' => $input_text
    ]));
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/json']);
    curl_setopt($ch, CURLOPT_TIMEOUT, 10);
    $response = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_HTTP_CODE);

    if ($status === 200 && $response) {
        $json = json_decode($response, true);
        if (isset($json['embeddings'][0])) {
            return $json['embeddings'][0];
        }
    }

    throw new Exception("Failed to get embedding from Ollama (Status: $status). Response: " . substr($response, 0, 500));
}

// Convert float array to PostgreSQL vector literal format: [0.1,0.2,...]
function array_to_postgres_vector($array) {
    if (!is_array($array)) {
        return null;
    }
    return '[' . implode(',', array_map('floatval', $array)) . ']';
}

// Format tile document text for embedding alignment
function format_tile_document_text($name, $language, $tags, $summary) {
    // If $tags is string (e.g. from Postgres, or comma-separated), clean and parse it
    if (is_string($tags)) {
        $tags = trim($tags, '{}');
        $tags_array = array_filter(array_map('trim', explode(',', $tags)));
    } else {
        $tags_array = (array)$tags;
    }
    $tags_str = implode(' ', $tags_array);
    return "{$name} " . strtoupper($language) . ", {$tags_str}: {$summary}";
}

// Perform a language-resolved query (with optional semantic search)
function search_tiles($pref_lang, $query_string = null, $offset = 0, $limit = 20) {
    $db = get_db_connection();
    
    // If limit is 0 or less, treat as no limit (unlimited)
    $db_limit = ($limit <= 0) ? 999999 : (int)$limit;

    // Check if administrator is logged in to bypass visibility check
    $show_invisible = false;
    if (session_status() === PHP_SESSION_NONE) {
        session_start();
    }
    if (!empty($_SESSION['admin_logged_in'])) {
        $show_invisible = true;
    }

    // Set up fallback languages
    $fallback_lang = ($pref_lang === 'de') ? 'en' : 'de';
    
    if (!empty($query_string)) {
        // Append language code in all-caps to target alignment with document embedding format
        $search_text = $query_string . ' ' . strtoupper($pref_lang);
        
        // Embed the query using query prefix
        $query_vector = get_embedding($search_text, 'query');
        $vector_str = array_to_postgres_vector($query_vector);
        
        $sql = "
            WITH resolved_tiles AS (
                SELECT *,
                       (embedding <=> :vector::vector) as distance,
                       ROW_NUMBER() OVER (
                           PARTITION BY name 
                           ORDER BY 
                               CASE WHEN language = :pref_lang THEN 1 
                                    WHEN language = :fallback_lang THEN 2 
                                    ELSE 3 
                               END
                       ) as rn
                FROM tiles
                WHERE (visible = true OR :show_invisible = true)
            )
            SELECT id, name, language, tags, title, html_teaser, 
                   summary, link, type, content_file, 
                   visible, accent_color, background, created_at, updated_at, distance
            FROM resolved_tiles 
            WHERE rn = 1 
            ORDER BY distance ASC 
            LIMIT :limit OFFSET :offset
        ";
        
        $stmt = $db->prepare($sql);
        $stmt->bindValue(':vector', $vector_str, PDO::PARAM_STR);
        $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
        $stmt->bindValue(':fallback_lang', $fallback_lang, PDO::PARAM_STR);
        $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
        $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
        $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
        $stmt->execute();
        return $stmt->fetchAll();
    } else {
        // No search query: order by sort_order / default sorting
        $sql = "
            WITH resolved_tiles AS (
                SELECT *,
                       ROW_NUMBER() OVER (
                           PARTITION BY name 
                           ORDER BY 
                               CASE WHEN language = :pref_lang THEN 1 
                                    WHEN language = :fallback_lang THEN 2 
                                    ELSE 3 
                               END
                       ) as rn
                FROM tiles
                WHERE (visible = true OR :show_invisible = true)
            )
            SELECT id, name, language, tags, title, html_teaser, 
                   summary, link, type, content_file, 
                   visible, accent_color, background, created_at, updated_at, sort_order
            FROM resolved_tiles 
            WHERE rn = 1 
            ORDER BY sort_order ASC, created_at DESC
            LIMIT :limit OFFSET :offset
        ";
        
        $stmt = $db->prepare($sql);
        $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
        $stmt->bindValue(':fallback_lang', $fallback_lang, PDO::PARAM_STR);
        $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
        $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
        $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
        $stmt->execute();
        return $stmt->fetchAll();
    }
}

// Find similar tiles for the "See Also" section (language-resolved)
function get_similar_tiles($name, $pref_lang, $limit = 3, $offset = 0) {
    $db = get_db_connection();
    $fallback_lang = ($pref_lang === 'de') ? 'en' : 'de';
    $db_limit = ($limit <= 0) ? 999999 : (int)$limit;

    $show_invisible = false;
    if (session_status() === PHP_SESSION_NONE) {
        session_start();
    }
    if (!empty($_SESSION['admin_logged_in'])) {
        $show_invisible = true;
    }

    $sql = "
        WITH source_tile AS (
            SELECT embedding 
            FROM tiles 
            WHERE name = :name 
            ORDER BY CASE WHEN language = :pref_lang THEN 1 WHEN language = :fallback_lang THEN 2 ELSE 3 END
            LIMIT 1
        ),
        resolved_tiles AS (
            SELECT *,
                   (embedding <=> (SELECT embedding FROM source_tile)) as distance,
                   ROW_NUMBER() OVER (
                       PARTITION BY name 
                       ORDER BY 
                           CASE WHEN language = :pref_lang THEN 1 
                                WHEN language = :fallback_lang THEN 2 
                                ELSE 3 
                           END
                   ) as rn
            FROM tiles
            WHERE (visible = true OR :show_invisible = true) AND name != :name
        )
        SELECT id, name, language, tags, title, html_teaser, 
               summary, link, type, content_file, 
               visible, accent_color, background, created_at, updated_at, distance
        FROM resolved_tiles 
        WHERE rn = 1 AND (SELECT embedding FROM source_tile) IS NOT NULL
        ORDER BY distance ASC 
        LIMIT :limit OFFSET :offset
    ";

    $stmt = $db->prepare($sql);
    $stmt->bindValue(':name', $name, PDO::PARAM_STR);
    $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
    $stmt->bindValue(':fallback_lang', $fallback_lang, PDO::PARAM_STR);
    $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
    $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
    $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
    $stmt->execute();
    return $stmt->fetchAll();
}

// Call local LLM completions / chat completions endpoint
function call_llm($system_prompt, $user_content) {
    $url = LLM_URL;
    if (strpos($url, '/chat/completions') === false && strpos($url, '/completions') === false) {
        $url = rtrim($url, '/') . '/v1/chat/completions';
    }

    $ch = curl_init($url);
    $headers = [
        'Content-Type: application/json'
    ];
    if (defined('LLM_PASS') && LLM_PASS !== '') {
        if (defined('LLM_USER') && LLM_USER !== '') {
            $headers[] = 'Authorization: Basic ' . base64_encode(LLM_USER . ':' . LLM_PASS);
        } else {
            $headers[] = 'Authorization: Bearer ' . LLM_PASS;
        }
    }

    $data = [
        'model' => LLM_MODEL,
        'messages' => [
            ['role' => 'system', 'content' => $system_prompt],
            ['role' => 'user', 'content' => $user_content]
        ],
        'temperature' => 0.3
    ];

    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($data));
    curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
    curl_setopt($ch, CURLOPT_TIMEOUT, 60);
    $response = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_HTTP_CODE);

    if ($status === 200 && $response) {
        $json = json_decode($response, true);
        if (isset($json['choices'][0]['message']['content'])) {
            return $json['choices'][0]['message']['content'];
        }
    }

    throw new Exception("LLM call failed (Status: $status). Response: " . substr($response, 0, 500));
}

// Determine translation health and source of truth for a given tile name or all tiles
function get_translation_status($tile_name = null) {
    $db = get_db_connection();
    
    if ($tile_name) {
        $stmt = $db->prepare("SELECT * FROM tiles WHERE name = :name ORDER BY created_at ASC");
        $stmt->execute([':name' => $tile_name]);
        $rows = $stmt->fetchAll();
    } else {
        $stmt = $db->prepare("SELECT * FROM tiles ORDER BY name ASC, created_at ASC");
        $stmt->execute();
        $rows = $stmt->fetchAll();
    }

    if (empty($rows)) {
        return [];
    }

    // Group rows by tile name
    $grouped = [];
    foreach ($rows as $row) {
        $grouped[$row['name']][] = $row;
    }

    $results = [];
    $contents_dir = __DIR__ . '/content/';

    foreach ($grouped as $name => $siblings) {
        // Oldest created_at sibling is the source of truth
        $source_row = $siblings[0];
        $source_lang = $source_row['language'];
        $source_db_mtime = strtotime($source_row['updated_at']);

        // Check source content file mtime if present
        $source_file_mtime = 0;
        if (!empty($source_row['content_file'])) {
            $source_file_path = $contents_dir . $source_row['content_file'];
            if (file_exists($source_file_path)) {
                $source_file_mtime = filemtime($source_file_path);
            }
        }
        $effective_source_mtime = max($source_db_mtime, $source_file_mtime);

        $lang_matrix = [];
        $has_stale = false;

        foreach ($siblings as $sibling) {
            $s_lang = $sibling['language'];
            $is_source = ($s_lang === $source_lang);

            $s_db_mtime = strtotime($sibling['updated_at']);
            $s_file_mtime = 0;
            if (!empty($sibling['content_file'])) {
                $s_file_path = $contents_dir . $sibling['content_file'];
                if (file_exists($s_file_path)) {
                    $s_file_mtime = filemtime($s_file_path);
                }
            }

            $status = 'up_to_date';
            if ($is_source) {
                $status = 'source';
            } else if ($s_db_mtime < $effective_source_mtime) {
                $status = 'stale';
                $has_stale = true;
            }

            $lang_matrix[$s_lang] = [
                'id' => (int)$sibling['id'],
                'language' => $s_lang,
                'is_source' => $is_source,
                'status' => $status,
                'created_at' => $sibling['created_at'],
                'updated_at' => $sibling['updated_at'],
                'content_file' => $sibling['content_file'],
                'file_mtime' => $s_file_mtime ? date('Y-m-d H:i:s', $s_file_mtime) : null
            ];
        }

        $results[$name] = [
            'name' => $name,
            'source_language' => $source_lang,
            'effective_source_mtime' => date('Y-m-d H:i:s', $effective_source_mtime),
            'has_stale_translation' => $has_stale,
            'languages' => $lang_matrix
        ];
    }

    return $tile_name ? ($results[$tile_name] ?? null) : $results;
}


